# localhoist/laravel

`php artisan share` — the Laravel-native entry point for
[localhoist](https://github.com/xPapay/localhoist). Puts your local dev
environment online with Vite HMR, Reverb websockets, and signed URLs all
working through one tunnel, zero config.

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

## How it finds the binary

The command is a thin wrapper around the `localhoist` Go binary
(tailwindcss-standalone pattern). Resolution order:

1. `LOCALHOIST_BINARY` environment variable
2. `localhoist` on your `PATH`
3. `~/.localhoist/bin/` cache
4. Downloaded from the matching GitHub release (first run only)

Until binaries are published, build from source and use option 1 or 2:
`go build ./cmd/localhoist`.

Note for Sail users: run this on your **host**, not inside the container —
the tunnel needs the host's published ports and your host ngrok install.
The command warns when it detects a container.
