// Command localhoist shares a local Laravel dev environment through a tunnel —
// app, Vite HMR, and Reverb websockets all working through one public URL,
// zero config. Port of the hand-rolled bin/expose + nginx mux setup.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/mdp/qrterminal/v3"

	"github.com/xPapay/localhoist/internal/envfile"
	"github.com/xPapay/localhoist/internal/laravel"
	"github.com/xPapay/localhoist/internal/proxy"
	"github.com/xPapay/localhoist/internal/tunnel"
)

// version is stamped by releases via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "localhoist: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", ".", "Laravel project directory")
	domain := flag.String("domain", "", "static tunnel domain (overrides NGROK_TUNNEL_URL)")
	appFlag := flag.String("app", "", "app upstream URL (overrides the one derived from APP_URL)")
	noQR := flag.Bool("no-qr", false, "skip the QR code")
	noPatch := flag.Bool("no-env-patch", false, "don't touch .env (URLs/websockets may break)")
	forcePatch := flag.Bool("env-patch", false, "patch .env even when the localhoist/laravel middleware is installed (e.g. for URLs in queued emails)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("localhoist " + version)
		return nil
	}

	// ── Detect project ───────────────────────────────────────────────

	// If a previous run crashed, its state file is still there — restore
	// BEFORE reading config, so we never derive upstreams from a stale
	// tunnel URL.
	envPath := *dir + "/.env"
	if st, err := envfile.LoadState(envPath); err != nil {
		return err
	} else if st != nil {
		if err := st.Restore(); err != nil {
			return fmt.Errorf("restoring .env from a previous crashed run: %w", err)
		}
		fmt.Println("  ↺ restored .env values left over from a previous run")
	}

	project, err := laravel.Detect(*dir)
	if err != nil {
		return err
	}
	if *appFlag != "" {
		u, err := url.Parse(*appFlag)
		if err != nil || u.Host == "" {
			return fmt.Errorf("--app %q is not a valid URL", *appFlag)
		}
		project.AppUpstream = u
	}
	if *domain == "" {
		*domain = project.StaticDomain
	}

	// ── Start the local mux ──────────────────────────────────────────

	cfg := proxy.Config{
		App:     project.AppUpstream,
		Vite:    project.ViteUpstream,
		Reverb:  project.ReverbUpstream,
		HotFile: filepath.Join(project.Dir, "public", "hot"),
	}
	routes := cfg.Routes()
	mux := proxy.NewMux(cfg)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	muxPort := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{Handler: mux}
	go server.Serve(listener)

	// ── Open the tunnel ──────────────────────────────────────────────

	fmt.Printf("  ⣾ starting tunnel to 127.0.0.1:%d …\n", muxPort)
	ctx := context.Background()
	tun, err := tunnel.StartNgrok(ctx, muxPort, *domain)
	if err != nil {
		return err
	}

	// ── Patch .env, with cleanup wired up first ──────────────────────

	var state *envfile.State
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if state != nil {
				if err := state.Restore(); err != nil {
					fmt.Fprintf(os.Stderr, "localhoist: FAILED to restore .env: %v\n", err)
					fmt.Fprintf(os.Stderr, "localhoist: original values are in %s\n", envfile.StatePath(project.Env.Path))
				} else {
					fmt.Println("  ✔ .env restored")
				}
			}
			tun.Stop()
		})
	}
	defer cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Only APP_URL needs patching — Echo/Reverb browser config is rewritten
	// in-flight, and the backend keeps publishing events to Reverb over
	// localhost. With the localhoist/laravel middleware installed (>= 0.2),
	// Laravel derives URLs from the tunnel's X-Forwarded-* headers and even
	// APP_URL can stay untouched.
	patchEnv := !*noPatch && (*forcePatch || !project.TrustedProxyPackage)
	patched := false
	if patchEnv {
		state, err = envfile.SaveState(project.Env, []string{"APP_URL"})
		if err != nil {
			return err
		}
		if project.Env.Set("APP_URL", tun.URL) {
			patched = true
			if err := project.Env.Save(); err != nil {
				return err
			}
		}
	}

	// ── Banner ───────────────────────────────────────────────────────

	fmt.Println()
	fmt.Printf("  ✔ %s\n", project.Dir)
	for _, r := range routes {
		fmt.Printf("      %-14s → %s (%s)\n", r.Prefix, r.Target, r.Name)
	}
	if patched {
		fmt.Println("  ✔ .env patched: APP_URL (restored on exit)")
	} else if !*noPatch && project.TrustedProxyPackage {
		fmt.Println("  ✔ zero .env mutation — localhoist/laravel middleware derives URLs from the tunnel")
	}
	fmt.Println()
	fmt.Printf("  🌍 %s\n", tun.URL)
	fmt.Println()

	if project.ConfigCached && patched {
		fmt.Println("  ⚠ config is cached — run `php artisan config:clear` or the app won't see the new APP_URL")
	}
	fmt.Println("  ✔ HMR + Echo rewritten in-flight — no Vite restart needed")
	fmt.Println()

	if !*noQR {
		qrterminal.GenerateHalfBlock(tun.URL, qrterminal.L, os.Stdout)
		fmt.Println()
	}
	if patched {
		fmt.Println("  Ctrl+C stops the tunnel and restores .env")
	} else {
		fmt.Println("  Ctrl+C stops the tunnel")
	}

	// ── Wait ─────────────────────────────────────────────────────────

	select {
	case <-sigCh:
		fmt.Println()
	case <-tun.Done:
		cleanup()
		return fmt.Errorf("tunnel process exited unexpectedly: %v", tun.Err)
	}
	return nil
}
