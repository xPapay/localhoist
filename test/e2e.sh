#!/usr/bin/env bash
# End-to-end harness for the localhoist binary. Uses fake cloudflared/ngrok
# binaries on PATH and an isolated XDG_CONFIG_HOME, so it runs entirely
# offline, publishes nothing, and never touches the developer's real config.
#
#   Scenario 1: clean run (default cloudflare transport) patches APP_URL only,
#               shows the transport hint + the middleware pointer, SIGINT
#               restores byte-for-byte
#   Scenario 1b: config cached → the middleware pointer moves to the warning,
#                and is shown only once
#   Scenario 2: SIGKILL mid-run, next start recovers the original values
#   Scenario 3: --transport ngrok uses ngrok; no save-default prompt off-TTY
#   Scenario 4: --domain implies ngrok when no transport is configured
#   Scenario 5: config precedence — global set/unset, project file overrides
#   Scenario 6: missing transport binary fails cleanly, .env untouched
#   Scenario 7: the mux proxies real requests and 502s dead upstreams
#   Scenario 8: localhoist/laravel >= 0.2 in composer.lock → zero .env mutation
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
FAKEBIN="$WORK/fakebin"
PROJ="$WORK/fakeproj"
mkdir -p "$FAKEBIN" "$PROJ" "$WORK/xdg"

# Isolate config: no run may read or write ~/.config/localhoist.
export XDG_CONFIG_HOME="$WORK/xdg"

EXPOSE_PID=""
HTTP_PID=""
cleanup() {
  [ -n "$EXPOSE_PID" ] && kill "$EXPOSE_PID" 2>/dev/null || true
  if [ -n "$HTTP_PID" ]; then
    kill "$HTTP_PID" 2>/dev/null || true
    wait "$HTTP_PID" 2>/dev/null || true
  fi
  pkill -f "$FAKEBIN/ngrok" 2>/dev/null || true
  pkill -f "$FAKEBIN/cloudflared" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# ── fake transports ───────────────────────────────────────────────────
# Both reap their sleep child on TERM so no orphans hold the stdout pipe open.
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

# cloudflared logs its banner to stderr — the binary must merge the streams.
cat > "$FAKEBIN/cloudflared" <<'EOF'
#!/usr/bin/env bash
trap 'kill $SLEEP_PID 2>/dev/null; exit 0' TERM INT
echo '2026-07-15T10:00:00Z INF Thank you for trying Cloudflare Tunnel. https://www.cloudflare.com/website-terms/' >&2
echo '2026-07-15T10:00:01Z INF +--------------------------------------------------------------------------------------------+' >&2
echo '2026-07-15T10:00:01Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |' >&2
echo '2026-07-15T10:00:01Z INF |  https://fake-words-here.trycloudflare.com                                                 |' >&2
echo '2026-07-15T10:00:01Z INF +--------------------------------------------------------------------------------------------+' >&2
sleep 300 &
SLEEP_PID=$!
wait $SLEEP_PID
EOF
chmod +x "$FAKEBIN/cloudflared"

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

(cd "$REPO" && go build -o "$WORK/localhoist" ./cmd/localhoist)

run_expose() {
  : > "$WORK/out.log"
  PATH="$FAKEBIN:$PATH" "$WORK/localhoist" --dir "$PROJ" --no-qr "$@" > "$WORK/out.log" 2>&1 &
  EXPOSE_PID=$!
  for i in $(seq 1 50); do
    grep -q "🌍" "$WORK/out.log" 2>/dev/null && break   # banner prints after the .env patch
    sleep 0.1
  done
}

fail() { echo "FAIL: $*"; echo "--- output ---"; cat "$WORK/out.log"; exit 1; }

# ── Scenario 1: clean run (cloudflare default) + SIGINT restore ──────
run_expose
grep -q "starting Cloudflare quick tunnel" "$WORK/out.log" || fail "default transport is not the cloudflare quick tunnel"
grep -q "APP_URL=https://fake-words-here.trycloudflare.com" "$PROJ/.env" || fail "APP_URL not patched"
# REVERB_* must stay untouched: the browser config is rewritten in-flight
# and the backend publishes to Reverb over localhost.
grep -q 'REVERB_HOST="localhost"' "$PROJ/.env" || fail "REVERB_HOST was patched — it must not be"
grep -q "REVERB_PORT=8080"        "$PROJ/.env" || fail "REVERB_PORT was patched — it must not be"
[ -f "$PROJ/.env.localhoist-state.json" ] || fail "state file missing while running"
# Zero-config runs point at the one choice worth knowing about.
grep -q "Prefer ngrok" "$WORK/out.log" || fail "transport hint missing on an unconfigured run"
# With the middleware package absent (no composer.lock) and config not cached,
# the patched line carries the "skip the edit for good" pointer to the package.
grep -q "skip the edit for good:  composer require --dev localhoist/laravel" "$WORK/out.log" \
  || fail "middleware hint missing on a patched run without the package"

kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after SIGINT"
[ ! -f "$PROJ/.env.localhoist-state.json" ] || fail "state file left behind after clean exit"
pgrep -f "$FAKEBIN/cloudflared" > /dev/null && fail "fake cloudflared still running after exit"
echo "PASS: clean run (cloudflare default) patches and restores .env"

# ── Scenario 1b: config cached moves the middleware pointer to the warning ──
mkdir -p "$PROJ/bootstrap/cache"
: > "$PROJ/bootstrap/cache/config.php"
run_expose
grep -q "config is cached" "$WORK/out.log" || fail "config-cached warning missing"
grep -q "or sidestep it entirely:  composer require --dev localhoist/laravel" "$WORK/out.log" \
  || fail "middleware pointer missing from the config-cached warning"
# The suggestion appears once: with config cached, the plain patched line drops it.
grep -q "skip the edit for good" "$WORK/out.log" && fail "middleware pointer duplicated when config is cached"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after config-cached run"
rm -rf "$PROJ/bootstrap"
echo "PASS: config cached — middleware pointer moves to the warning, shown once"

# ── Scenario 2: crash (SIGKILL) + recovery on next start ─────────────
run_expose
kill -9 "$EXPOSE_PID"; wait "$EXPOSE_PID" 2>/dev/null || true
pkill -f "$FAKEBIN/cloudflared" 2>/dev/null || true
grep -q "fake-words-here" "$PROJ/.env" || fail "precondition: .env should still be patched after SIGKILL"
[ -f "$PROJ/.env.localhoist-state.json" ] || fail "precondition: state file should survive SIGKILL"

run_expose   # next start must restore first, then re-patch
grep -q "restored .env values left over" "$WORK/out.log" || fail "no crash-recovery message"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after recovery run"
echo "PASS: crash recovery restores original values on next start"

# ── Scenario 3: --transport ngrok ─────────────────────────────────────
run_expose --transport ngrok
grep -q "starting ngrok tunnel" "$WORK/out.log" || fail "--transport ngrok did not pick ngrok"
grep -q "APP_URL=https://fake123.ngrok-free.app" "$PROJ/.env" || fail "APP_URL not patched with the ngrok URL"
# Not a TTY here — the save-default prompt must never fire off-terminal.
grep -q "Make ngrok your default" "$WORK/out.log" && fail "save-default prompt fired without a TTY"
grep -q "Prefer ngrok" "$WORK/out.log" && fail "transport hint shown on an ngrok run"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after ngrok run"
echo "PASS: --transport ngrok overrides the default, no prompt off-TTY"

# ── Scenario 4: a static domain implies ngrok ─────────────────────────
run_expose --domain my-app.ngrok-free.dev
grep -q "static domain my-app.ngrok-free.dev → using ngrok" "$WORK/out.log" || fail "domain-implies-ngrok note missing"
grep -q "starting ngrok tunnel" "$WORK/out.log" || fail "--domain did not route to ngrok"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true

# …but an *explicit* cloudflare choice + domain is a conflict, not a guess.
if PATH="$FAKEBIN:$PATH" "$WORK/localhoist" --dir "$PROJ" --no-qr --transport cloudflare --domain my-app.ngrok-free.dev > "$WORK/out.log" 2>&1; then
  fail "explicit cloudflare + domain should fail"
fi
grep -q "needs ngrok" "$WORK/out.log" || fail "conflict error message missing"
echo "PASS: static domain implies ngrok; explicit cloudflare + domain errors"

# ── Scenario 5: config precedence (global < project < flag) ──────────
"$WORK/localhoist" config set transport ngrok > /dev/null
run_expose
grep -q "starting ngrok tunnel" "$WORK/out.log" || fail "global config transport=ngrok not honored"
grep -q "Prefer ngrok" "$WORK/out.log" && fail "transport hint shown despite explicit config"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true

echo '{"transport": "cloudflare"}' > "$PROJ/.localhoist.json"
run_expose
grep -q "starting Cloudflare quick tunnel" "$WORK/out.log" || fail "project config did not override global"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true

# `config` (run from the project) must attribute the value to the project file.
# (grep -q on a pipe would SIGPIPE the binary under pipefail — go via a file.)
(cd "$PROJ" && "$WORK/localhoist" config > "$WORK/cfg.out")
grep -q "project config" "$WORK/cfg.out" || fail "config show does not attribute the project source: $(cat "$WORK/cfg.out")"
rm "$PROJ/.localhoist.json"
"$WORK/localhoist" config unset transport > /dev/null
run_expose
grep -q "starting Cloudflare quick tunnel" "$WORK/out.log" || fail "unset did not return to the default"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
echo "PASS: config precedence — global honored, project overrides, unset reverts"

# ── Scenario 6: missing transport binary fails cleanly ───────────────
if PATH="/usr/bin:/bin" "$WORK/localhoist" --dir "$PROJ" --no-qr > "$WORK/out.log" 2>&1; then
  fail "run without cloudflared on PATH should fail"
fi
grep -q "cloudflared not found in PATH" "$WORK/out.log" || fail "missing-binary error message absent"
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env touched even though the tunnel never started"
echo "PASS: missing transport binary fails cleanly, .env untouched"

# ── Scenario 7: mux routes real requests and rewrites HTML in-flight ─
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

# ── Scenario 8: middleware package installed → zero .env mutation ────
cat > "$PROJ/composer.lock" <<'EOF'
{"packages": [], "packages-dev": [{"name": "localhoist/laravel", "version": "v0.2.0"}]}
EOF

run_expose
grep -q "zero .env mutation" "$WORK/out.log" || fail "zero-mutation banner missing"
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env was touched despite the middleware package"
[ ! -f "$PROJ/.env.localhoist-state.json" ] && echo ok > /dev/null || fail "state file created despite zero mutation"
# Package present → nothing to suggest.
grep -q "composer require --dev localhoist/laravel" "$WORK/out.log" && fail "middleware hint shown though the package is installed"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true

# --env-patch must force the old behavior even with the package installed.
: > "$WORK/out.log"
PATH="$FAKEBIN:$PATH" "$WORK/localhoist" --dir "$PROJ" --no-qr --env-patch > "$WORK/out.log" 2>&1 &
EXPOSE_PID=$!
for i in $(seq 1 50); do grep -q "🌍" "$WORK/out.log" 2>/dev/null && break; sleep 0.1; done
grep -q "APP_URL=https://fake-words-here.trycloudflare.com" "$PROJ/.env" || fail "--env-patch did not force patching"
# Forced patch with the package installed patches APP_URL but must NOT suggest
# installing what's already there — the hint is gated on the package's absence.
grep -q "skip the edit for good" "$WORK/out.log" && fail "middleware hint shown on a forced --env-patch run with the package installed"
kill -INT "$EXPOSE_PID"; wait "$EXPOSE_PID" || true
rm -f "$PROJ/composer.lock"
diff -u "$PROJ/.env.orig" "$PROJ/.env" || fail ".env not restored after forced patch run"
echo "PASS: middleware package enables zero .env mutation (--env-patch overrides)"

echo
echo "ALL E2E SCENARIOS PASSED"
