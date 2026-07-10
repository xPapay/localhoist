# Handoff: Productize `bin/expose` — "artisan share that actually works"

## Goal of next session

Continue from a product-design discussion toward building an MVP of a sellable dev tool: one command that exposes a developer's localhost online (e.g. to open on their phone) with **Vite HMR, Reverb/websockets, and signed URLs all working through a single tunnel, zero config**. Laravel ecosystem first.

## Context: where the idea comes from

The user's repo (`/Users/lukas/Documents/Projects/personeo-2`, Laravel 12 + Vue/Inertia + Sail + Reverb + Vite) contains a hand-built version of this:

- **`bin/expose`** — bash script that starts an ngrok tunnel to the nginx port, polls `127.0.0.1:4040/api/tunnels` for the public URL, then patches `.env` in place (`APP_URL`, `REVERB_HOST`, `REVERB_PORT=443`, `REVERB_SCHEME=https`) and restores the originals via `trap cleanup EXIT`. Reads `NGINX_PORT` (default 8088) and optional `NGROK_TUNNEL_URL` (static domain) from `.env`/environment.
- **`docker/nginx/templates/sail-proxy.conf.template`** + nginx service in `docker-compose.yml` — the crucial multiplexer that makes ONE tunneled port serve everything:
  - `/` → Laravel app container (port 80)
  - `/@vite/`, `/@id/`, `/@fs/`, `/resources/`, `/node_modules/`, `/vendor/`, `/__vite_hmr` → Vite dev server (`VITE_PORT`), with ws upgrade for HMR
  - `/app` → Reverb websocket server (`REVERB_SERVER_PORT`), with ws upgrade
  - `/proxy-realtime` → TLS/SNI proxy to OpenAI Realtime API (strips Authorization header; ephemeral tokens generated on BE)
  - `/proxy-gemini-live` → same idea for `generativelanguage.googleapis.com`
  - The realtime proxies are bespoke to this repo's voice-chat work; treat as evidence users need a custom-routes escape hatch, not core product scope.

Key insight: ngrok tunnels ONE port; without the nginx mux, HMR/Reverb/app would each need separate tunnels. The product must internalize the mux.

## The pitch & moat

The tunnel itself is commodity (ngrok, cloudflared, BeyondCode Expose). The moat is **framework-awareness**: sharing a Vite+Reverb Laravel app through a plain tunnel breaks in three places — HMR ws points at `localhost:5173`, Echo points at `localhost:8080`, `APP_URL` mismatch breaks signed URLs/assets. Nobody fixes all three today. Incumbent to position against: **BeyondCode Expose** ("Expose gives you a tunnel; we make your app actually *work* through it").

## Agreed architecture (3 layers)

1. **Tunnel transport** — commodity; BYO ngrok/cloudflared for free tier, own relay later (build on `frp`/`rathole` or custom Go). Don't innovate here.
2. **Smart local mux** — reverse proxy embedded in the client binary replacing nginx: path routing with ws upgrade for app/Vite/Reverb, plus the key trick: **rewrite the `/@vite/client` response body in-flight** to point HMR at the tunnel host (Vite bakes HMR host at dev-server start; rewriting avoids Vite restarts). Config escape hatch for custom routes.
3. **Framework adapter (Laravel first)** — detect `artisan`/`.env`, read `VITE_PORT`, `REVERB_SERVER_PORT`, `APP_PORT`, detect Sail vs Valet vs Herd, auto-build the route map.
   - v1: patch-and-restore `.env` (harden the existing script's approach: crash-safe backup file, `config:cache` detection).
   - v2: `expose-ready` composer package — trusted-proxy middleware deriving URLs from `X-Forwarded-*` + JS helper configuring Echo from `window.location`. Zero mutation; also a distribution wedge.

## Distribution forms (ship in this order)

1. **Single Go/Rust binary CLI** (core; everything wraps it; `brew install` / `curl | sh`)
2. **Composer package → `php artisan share`** — thin wrapper auto-downloading the binary (Tailwind-standalone pattern); the Laravel-native entry point
3. **Docker/Sail service** — same binary containerized, joins `sail` network; 5 lines in docker-compose
4. **Desktop tray app (Tauri)** — later; tunnels list, request inspector, QR code

**QR code in terminal output is the 10-second wow** for the "open on phone" headline use case — cheap, disproportionate value.

## Monetization

Open-core tunnel model: Free (BYO transport or random subdomains, session limits) / Pro $5–10/mo (stable custom subdomains — critical for OAuth/webhooks — password-protected tunnels, request inspector/replay, multiple tunnels) / Team (shared access). Warning flagged: hosted relay = phishing-abuse target; plan random-subdomains-by-default, abuse reporting, takedown from day one.

## MVP path

1. **Week 1–2**: Go binary = port of `bin/expose` + embedded mux (replaces nginx) + Laravel `.env` adapter + QR code; transport BYO ngrok. Dogfood in personeo-2 (could delete nginx service + `bin/expose`).
2. **Week 3–4**: `php artisan share` composer wrapper + `/@vite/client` rewrite + crash-safe restore.
3. **Then**: own relay + custom domains (start charging), Sail docker image, `expose-ready` package, tray app.

Validate before building the relay: ship steps 1–2 free, watch adoption.

## Open decisions (user has NOT chosen yet)

- Go vs Rust for the binary (Go was leaned toward in discussion)
- Product name
- Start with MVP binary vs `expose-ready` composer package first (last question posed to user, unanswered)
- Whether this lives in a new repo (almost certainly yes — nothing built yet, zero code written)

## State

**No code has been written.** This is pure design/ideation so far. The next session starts from scratch on implementation (or further spec work).

## Suggested skills for next session

- `to-prd` or `prd` — if the user wants the concept formalized before building
- `grill-me` / `interview-me` — to stress-test scope, pricing, and v1 cut before committing
- `spec-driven-development` or `to-issues` — to break the MVP into tracer-bullet issues
- If building starts: `tdd`, and reference `bin/expose` + `docker/nginx/templates/sail-proxy.conf.template` in personeo-2 as the source patterns
