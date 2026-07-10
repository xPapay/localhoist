# expose/laravel-share

`php artisan share` — the Laravel-native entry point for
[expose](../../README.md). Puts your local dev environment online with Vite
HMR, Reverb websockets, and signed URLs all working through one tunnel,
zero config.

## Install

```sh
composer require --dev expose/laravel-share
```

While the package is unpublished, use a path repository:

```sh
composer config repositories.expose path /path/to/expose/packages/laravel-share
composer require --dev "expose/laravel-share:*@dev"
```

## Usage

```sh
php artisan share
php artisan share --domain=my-app.ngrok-free.dev
php artisan share --no-qr
```

## How it finds the binary

The command is a thin wrapper around the `expose` Go binary
(tailwindcss-standalone pattern). Resolution order:

1. `EXPOSE_BINARY` environment variable
2. `expose` on your `PATH`
3. `~/.expose/bin/` cache
4. Downloaded from the matching GitHub release (first run only)

Until binaries are published, build from source and use option 1 or 2:
`go build ./cmd/expose`.

Note for Sail users: run this on your **host**, not inside the container —
the tunnel needs the host's published ports and your host ngrok install.
The command warns when it detects a container.
