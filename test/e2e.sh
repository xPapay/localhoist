#!/usr/bin/env bash
# End-to-end harness for the expose binary. Uses a fake ngrok on PATH, so it
# runs entirely offline and publishes nothing.
#
#   Scenario 1: clean run patches .env, SIGINT restores it byte-for-byte
#   Scenario 2: SIGKILL mid-run, next start recovers the original values
#   Scenario 3: the mux proxies real requests and 502s dead upstreams
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
FAKEBIN="$WORK/fakebin"
PROJ="$WORK/fakeproj"
mkdir -p "$FAKEBIN" "$PROJ"

EXPOSE_PID=""
HTTP_PID=""
cleanup() {
  [ -n "$EXPOSE_PID" ] && kill "$EXPOSE_PID" 2>/dev/null || true
  if [ -n "$HTTP_PID" ]; then
    kill "$HTTP_PID" 2>/dev/null || true
    wait "$HTTP_PID" 2>/dev/null || true
  fi
  pkill -f "$FAKEBIN/ngrok" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# ── fake ngrok ────────────────────────────────────────────────────────
# Reaps its sleep child on TERM so no orphans hold the stdout pipe open.
cat > "$FAKEBIN/ngrok" <<'EOF'
#!/usr/bin/env bash
trap 'kill $SLEEP_PID 2>/dev/null; exit 0' TERM INT
echo '{"lvl":"info","msg":"client session established"}'
echo '{"lvl":"info","msg":"started tunnel","name":"command_line","url":"https://fake123.ngrok-free.app"}'
sleep 300 &
SLEEP_PID=$!
wait $SLEEP_PID
EOF
chmod +x "$FAKEBIN/ngrok"

# ── fake Laravel project ──────────────────────────────────────────────
printf '#!/usr/bin/env php\n' > "$PROJ/artisan"
# VITE_PORT deliberately points at a dead port — a real dev machine may
# have an actual Vite running on 5173, which would break the 502 assertion.
cat > "$PROJ/.env" <<'EOF'
APP_NAME=Fake
APP_URL=http://localhost:8088
VITE_PORT=18098
REVERB_APP_KEY=key123
REVERB_HOST="localhost"
REVERB_PORT=8080
REVERB_SCHEME=http
EOF
cp "$PROJ/.env" "$PROJ/.env.orig"

(cd "$REPO" && go build -o "$WORK/expose" ./cmd/expose)

run_expose() {
  : > "$WORK/out.log"
  PATH="$FAKEBIN:$PATH" "$WORK/expose" --dir "$PROJ" --no-qr > "$WORK/out.log" 2>&1 &
  EXPOSE_PID=$!
  for i in $(seq 1 50); do
    grep -q "🌍" "$WORK/out.log" 2>/dev/null && break   # banner prints after the .env patch
    sleep 0.1
  done
}

fail() { echo "FAIL: $*"; echo "--- output ---"; cat "$WORK/out.log"; exit 1; }

# ── Scenario 1: clean run + SIGINT restore ───────────────────────────
run_expose
grep -q "APP_URL=https://fake123.ngrok-free.app" "$PROJ/.env" || fail "APP_URL not patched"
grep -q "REVERB_HOST=fake123.ngrok-free.app"     "$PROJ/.env" || fail "REVERB_HOST not patched"
grep -q "REVERB_PORT=443"                        "$PROJ/.env" || fail "REVERB_PORT not patched"
grep -q "REVERB_SCHEME=https"                    "$PROJ/.env" || fail "REVERB_SCHEME not patched"
[ -f "$PROJ/.env.expose-state.json" ] || fail "state file missing while running"

kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after SIGINT"
[ ! -f "$PROJ/.env.expose-state.json" ] || fail "state file left behind after clean exit"
pgrep -f "$FAKEBIN/ngrok" > /dev/null && fail "fake ngrok still running after exit"
echo "PASS: clean run patches and restores .env"

# ── Scenario 2: crash (SIGKILL) + recovery on next start ─────────────
run_expose
kill -9 "$EXPOSE_PID"; wait "$EXPOSE_PID" 2>/dev/null || true
pkill -f "$FAKEBIN/ngrok" 2>/dev/null || true
grep -q "fake123" "$PROJ/.env" || fail "precondition: .env should still be patched after SIGKILL"
[ -f "$PROJ/.env.expose-state.json" ] || fail "precondition: state file should survive SIGKILL"

run_expose   # next start must restore first, then re-patch
grep -q "restored .env values left over" "$WORK/out.log" || fail "no crash-recovery message"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after recovery run"
echo "PASS: crash recovery restores original values on next start"

# ── Scenario 3: mux routes real requests and rewrites HTML in-flight ─
DOCROOT="$WORK/docroot"
mkdir -p "$DOCROOT" "$PROJ/public"
cat > "$DOCROOT/index.html" <<'EOF'
<html><head>
<script type="module" src="http://[::1]:5173/@vite/client"></script>
<script type="module" src="http://[::1]:5173/resources/js/app.js"></script>
</head><body>ok</body></html>
EOF
printf 'http://[::1]:5173' > "$PROJ/public/hot"

python3 -m http.server 18099 --bind 127.0.0.1 -d "$DOCROOT" > /dev/null 2>&1 &
HTTP_PID=$!
sed -i.bak 's|^APP_URL=.*|APP_URL=http://127.0.0.1:18099|' "$PROJ/.env"; rm -f "$PROJ/.env.bak"
cp "$PROJ/.env" "$PROJ/.env.orig"

run_expose
MUX_PORT=$(grep -o 'tunnel to 127.0.0.1:[0-9]*' "$WORK/out.log" | grep -o '[0-9]*$')
[ -n "$MUX_PORT" ] || fail "could not find mux port in output"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$MUX_PORT/")
[ "$CODE" = "200" ] || fail "mux did not proxy / to the app upstream (got $CODE)"
CODE_VITE=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$MUX_PORT/@vite/client")
[ "$CODE_VITE" = "502" ] || fail "expected 502 from dead vite upstream, got $CODE_VITE"

# The hot-file origin in HTML must be rewritten to the public origin the
# browser used (Host header), so @vite tags work with no restart.
BODY=$(curl -s -H "Host: phone.example.test" -H "Accept: text/html" "http://127.0.0.1:$MUX_PORT/index.html")
echo "$BODY" | grep -q 'src="http://phone.example.test/@vite/client"' \
  || fail "hot origin not rewritten in HTML: $BODY"
echo "$BODY" | grep -q '\[::1\]:5173' && fail "hot origin survived in HTML: $BODY"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
echo "PASS: mux proxies requests, rewrites HTML in-flight, and 502s dead upstreams"

echo
echo "ALL E2E SCENARIOS PASSED"
