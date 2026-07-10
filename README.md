# localhoist

`artisan share` that actually works: one command that puts your local Laravel
dev environment online — app, **Vite HMR**, and **Reverb websockets** all
working through a single tunnel, zero config.

Plain tunnels (ngrok, cloudflared, BeyondCode Expose) break a modern Laravel
dev setup in three places: HMR keeps pointing at `localhost:5173`, Echo keeps
pointing at `localhost:8080`, and the `APP_URL` mismatch breaks signed URLs
and asset generation. This tool fixes all three.

## Status

Working binary + `php artisan share`, BYO ngrok transport, zero-config HMR/Echo through the tunnel. See [Roadmap](#roadmap).

## What exists now

- **`cmd/localhoist`** — the CLI. Detects the project, starts the mux on an
  ephemeral port, spawns your installed ngrok, patches `.env`, prints the
  public URL + QR code, and restores everything on `Ctrl+C`.
- **`internal/proxy`** — the smart mux that replaces the hand-rolled nginx
  service: `/app/` → Reverb, the Vite paths → the Vite dev server,
  everything else → the app, with websocket upgrades handled by Go's
  reverse proxy. Vite HMR websockets are additionally recognized by their
  `vite-hmr` subprotocol and routed correctly *whatever path they arrive
  on*, so no `hmr.path` config is needed. Laravel and Reverb see the public
  tunnel host and the edge's `X-Forwarded-*` headers, so URL generation
  keeps working; Vite gets its own local host (Vite 6+ rejects unknown
  hosts via `server.allowedHosts`). Stricter than nginx prefix locations
  where that matters — `/apple` never routes to Reverb.
- **`internal/rewrite`** — the in-flight response fixes that make "zero
  config, zero restart" real (shapes verified against Vite 5.4's shipped
  client): neutralizes the HMR host/port/protocol literals baked into
  `/@vite/client` so the browser derives them from the tunnel origin;
  rewrites the `VITE_REVERB_*` values in the `import.meta.env` object that
  Vite bakes into served modules, so Echo connects through the tunnel; and
  replaces the dev-server origin from `public/hot` in HTML responses, so
  `@vite` script tags point at the tunnel.
- **`internal/envfile`** — crash-safe `.env` patching. Original values are
  snapshotted to `.env.localhoist-state.json` *before* any write, so even after
  `kill -9` the next run restores the file byte-for-byte, quotes included.
  Layout, comments, and untouched keys are always preserved.
- **`internal/laravel`** — the framework adapter. Derives the app upstream
  from `APP_URL`/`APP_PORT`, so Sail (`localhost:8088`) and Herd/Valet
  (`myapp.test`) both work; the Reverb route switches on only when the
  project actually uses Reverb; warns when Laravel's config cache would
  swallow the `.env` patch.
- **`internal/tunnel`** — BYO transport. Spawns `ngrok` in its own process
  group and reads the tunnel URL from its JSON log stream instead of polling
  the local API port, so it can't collide with another running ngrok agent.
- **`packages/laravel`** — the Composer package, two things in one:
  `php artisan share`, a thin wrapper that locates the binary
  (`LOCALHOIST_BINARY` → PATH → `~/.localhoist/bin` cache → GitHub release
  download, tailwindcss-standalone style), hands it the real TTY, and
  passes flags through (warns Sail users when run inside a container);
  and the `TrustLocalhoistProxy` middleware, auto-registered in the
  `local` environment, which trusts the mux for loopback requests carrying
  its marker header — the binary detects the package via composer.lock and
  skips `.env` patching entirely.

## Usage

```sh
cd your-laravel-app
localhoist        # the binary directly…
php artisan share # …or via the Composer package (localhoist/laravel)
```

That's it. You get a public URL and a QR code to open it on your phone.
`Ctrl+C` stops the tunnel and restores everything.

Flags: `--dir` (project path), `--domain` (static tunnel domain, or set
`NGROK_TUNNEL_URL` in `.env`), `--app` (override the app upstream),
`--no-qr`, `--no-env-patch`.

## What it does

1. **Detects your project** — reads `.env` for `APP_URL`/`APP_PORT` (Sail),
   `VITE_PORT`, `REVERB_SERVER_PORT`; works with Sail, Herd, and Valet
   (`myapp.test` upstreams are derived from `APP_URL`).
2. **Starts a smart local mux** — a reverse proxy on an ephemeral port that
   routes one public hostname to all three local servers, with websocket
   upgrade where needed:
   | Path | Upstream |
   |---|---|
   | `/app/…` | Reverb (websockets) |
   | `/@vite/`, `/@id/`, `/@fs/`, `/resources/`, `/node_modules/`, `/vendor/`, `/__vite_hmr` | Vite dev server |
   | everything else | your app |
3. **Opens the tunnel** — v1 shells out to your installed `ngrok` (BYO
   transport; free).
4. **Rewrites dev-server responses in-flight** — this is what makes it work
   with a **stock `vite.config.js` and no Vite restart**. Vite bakes its
   connection config into what it serves at dev-server start; the mux fixes
   it as it passes through:
   - `/@vite/client`: the baked HMR host/port/protocol literals are
     neutralized so the browser derives them from the URL it loaded the
     script from — the tunnel. The HMR websocket is then routed home by its
     `vite-hmr` subprotocol, whatever its path.
   - served JS modules: the `VITE_REVERB_*` values in the baked
     `import.meta.env` object are pointed at the tunnel, so Laravel Echo
     connects without a restart.
   - HTML pages: the dev-server origin that `@vite` baked into script tags
     (from `public/hot`, e.g. `http://[::1]:5173`) is replaced with the
     tunnel origin.
5. **Keeps server-side URLs correct** — signed URLs, redirects, assets:
   - With `localhoist/laravel` (>= 0.2) installed: **zero `.env` mutation**.
     The mux stamps every request with an `X-Localhoist` marker; the
     package's middleware trusts the proxy only for loopback requests
     carrying that marker (and only in the `local` environment), so Laravel
     derives scheme + host from the tunnel's `X-Forwarded-*` headers.
     Spoofed forwarded headers without the marker stay ignored.
   - Without the package: `APP_URL` is patched while the tunnel runs. The
     original is snapshotted to `.env.localhoist-state.json` first, so even
     after a crash or `kill -9` the next run restores your file
     byte-for-byte. On exit the patch is reverted and the snapshot removed.
   - `REVERB_*` keys are never touched: the browser side is rewritten
     in-flight, and your backend keeps publishing events to Reverb over
     localhost instead of round-tripping through the tunnel.
   - Edge case: URLs generated outside a request (queued emails) still come
     from `APP_URL` — pass `--env-patch` to force the patch when you need
     those to point at the tunnel.

Tip: if you don't use the composer package, add `.env.localhoist-state.json`
to your project's `.gitignore` (it holds no secrets — just the original
`APP_URL`).

## Building and running

Prerequisites: Go 1.21+, and [ngrok](https://ngrok.com/download) installed
and authenticated (`ngrok config add-authtoken …`) — transport is BYO in
this version.

```sh
# install a released binary
go install github.com/xPapay/localhoist/cmd/localhoist@latest
# …or grab one from https://github.com/xPapay/localhoist/releases

# or build from source
go build -o localhoist ./cmd/localhoist
install -m 755 localhoist /usr/local/bin/localhoist

# run it from (or at) a Laravel project
cd ~/code/my-laravel-app && localhoist
localhoist --dir ~/code/my-laravel-app        # same thing, from anywhere
localhoist --domain my-app.ngrok-free.dev     # stable domain instead of a random one
```

While it runs: `.env` points at the tunnel, and HMR + Echo work through it
with no Vite restart and no `vite.config.js` changes. `Ctrl+C` restores
`.env` and closes the tunnel.

### Tests

```sh
go test ./...       # unit tests (envfile, laravel, proxy, rewrite, tunnel)
bash test/e2e.sh    # end-to-end: patch → run → restore, crash recovery,
                    # live routing + in-flight HTML rewriting through the
                    # mux, using a fake ngrok — runs offline, publishes nothing
```

## Roadmap

- [x] Go binary: embedded mux + Laravel `.env` adapter + QR code (BYO ngrok)
- [x] In-flight rewriting: `/@vite/client` HMR config, `import.meta.env`
      Reverb values, and `@vite` script-tag origins (no Vite restart, no
      config needed)
- [x] `php artisan share` composer wrapper (auto-downloads the binary)
- [x] Binary releases (GitHub) + Packagist split repo
- [x] Trusted-proxy middleware in `localhoist/laravel` — zero `.env`
      mutation (the planned `localhoist/ready` package, folded in; the JS
      Echo helper became unnecessary once the in-flight rewrite shipped)
- [ ] Custom route escape hatch (config file)
- [ ] Own relay + stable custom subdomains (paid tier)
- [ ] Sail/Docker service image
- [ ] Tray app (tunnels list, request inspector, QR)
