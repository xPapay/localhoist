# localhoist/laravel

`php artisan share` — the Laravel-native entry point for
[localhoist](https://github.com/xPapay/localhoist). Puts your local dev
environment online with Vite HMR, Reverb websockets, and signed URLs all
working through one tunnel, zero config. With this package installed,
localhoist also stops touching your `.env` entirely.

> Development happens in the [localhoist monorepo](https://github.com/xPapay/localhoist)
> (`packages/laravel`); this repository is a read-only split for Composer.
> Report issues there.

## Install

```sh
composer require --dev localhoist/laravel
```

If the package isn't on Packagist yet, install straight from the split
repository:

```sh
composer config repositories.localhoist vcs https://github.com/xPapay/localhoist-laravel
composer require --dev "localhoist/laravel:^0.1"
```

Hacking on the monorepo? Use a path repository instead:

```sh
composer config repositories.localhoist path /path/to/localhoist/packages/laravel
composer require --dev "localhoist/laravel:*@dev"
```

## Usage

```sh
php artisan share
php artisan share --domain=my-app.ngrok-free.dev
php artisan share --no-qr
```

## Which command? (it runs on your host)

The tunnel always runs on your **host machine** — that's where your ports
are published. `php artisan share` is just a launcher for the host binary,
so it needs your PHP to be on the host too:

| Where your PHP runs | Run |
| --- | --- |
| On the host | `php artisan share` |
| In a container | `localhoist` on the host, from your project directory (install it via Homebrew or `go install`) |

When PHP is containerized, `artisan` runs *inside* the container and can't
reach the host's published ports — so `php artisan share` detects this and
**stops with the host command to run instead** rather than failing halfway.
Have a non-standard setup that publishes ports differently? `php artisan
share --force` runs it anyway.

## Zero `.env` mutation

The package ships a `TrustLocalhoistProxy` middleware, auto-registered in
the `local` environment. The localhoist mux stamps every proxied request
with an `X-Localhoist` marker; the middleware trusts the proxy only when
the request comes from loopback **and** carries that marker. Laravel then
derives scheme and host from the tunnel's `X-Forwarded-*` headers, so
`url()`, `asset()`, redirects, and signed URLs are generated against the
public https origin — with `.env` untouched.

The binary detects this package in `composer.lock` (>= 0.2) and skips its
`.env` patching automatically. Forwarded headers on requests without the
marker are ignored, and nothing is trusted outside the `local` environment.

Edge case: URLs generated outside a request (e.g. links in queued emails)
still come from `APP_URL`. Run `php artisan share --env-patch` when you
need those pointing at the tunnel.

## How it finds the binary

The command is a thin wrapper around the `localhoist` Go binary
(tailwindcss-standalone pattern). Resolution order:

1. `LOCALHOIST_BINARY` environment variable
2. `localhoist` on your `PATH`
3. `~/.localhoist/bin/` cache
4. Downloaded from the matching GitHub release (first run only)

Until binaries are published, build from source and use option 1 or 2:
`go build ./cmd/localhoist`.
