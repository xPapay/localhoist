// Command localhoist shares a local Laravel dev environment through a tunnel —
// app, Vite HMR, and Reverb websockets all working through one public URL,
// zero config. Port of the hand-rolled bin/expose + nginx mux setup.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/mdp/qrterminal/v3"

	"github.com/xPapay/localhoist/internal/config"
	"github.com/xPapay/localhoist/internal/envfile"
	"github.com/xPapay/localhoist/internal/laravel"
	"github.com/xPapay/localhoist/internal/proxy"
	"github.com/xPapay/localhoist/internal/tunnel"
)

// version is stamped by releases via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "config" {
		if err := runConfig(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "localhoist: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "localhoist: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", ".", "Laravel project directory")
	transportFlag := flag.String("transport", "", "tunnel transport: cloudflare (quick tunnel, default) or ngrok")
	domain := flag.String("domain", "", "static tunnel domain (ngrok; overrides NGROK_TUNNEL_URL)")
	appFlag := flag.String("app", "", "app upstream URL (overrides the one derived from APP_URL)")
	noQR := flag.Bool("no-qr", false, "skip the QR code")
	noPatch := flag.Bool("no-env-patch", false, "don't touch .env (URLs/websockets may break)")
	forcePatch := flag.Bool("env-patch", false, "patch .env even when the localhoist/laravel middleware is installed (e.g. for URLs in queued emails)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  localhoist [flags]        share the Laravel project in the current directory\n  localhoist config …       show or change defaults (see `localhoist config help`)\n\nFlags:\n")
		flag.PrintDefaults()
	}
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

	// ── Pick the transport ───────────────────────────────────────────
	// --transport > .localhoist.json > ~/.config/localhoist > cloudflare.
	res, err := config.Resolve(*dir, *transportFlag)
	if err != nil {
		return err
	}
	res, transportNote, err := config.Finalize(res, *domain)
	if err != nil {
		return err
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

	if transportNote != "" {
		fmt.Printf("  ✔ %s\n", transportNote)
	}
	fmt.Printf("  ⣾ starting %s tunnel to 127.0.0.1:%d …\n", transportLabel(res.Transport), muxPort)
	ctx := context.Background()
	tun, err := tunnel.Start(ctx, res.Transport, muxPort, *domain)
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

	// Zero-config runs get a pointer to the one choice worth knowing about;
	// configured runs (any explicit choice, ever) don't get nagged.
	if res.Source == config.SourceDefault && res.Transport == config.TransportCloudflare {
		fmt.Println("  Cloudflare quick tunnel — free, no account, random URL each run.")
		fmt.Println("  Prefer ngrok (stable domains)?  localhoist config set transport ngrok")
		fmt.Println()
	}

	// The user overrode an unset default and it worked — the one moment
	// where offering to persist the choice is help, not nagging. Either
	// answer is written to the global config, so the question is asked
	// exactly once.
	if res.Source == config.SourceFlag && res.Saved == "" &&
		res.Transport != config.DefaultTransport && stdinIsTTY() {
		fmt.Printf("  Make %s your default transport? [y/N] ", res.Transport)
		ansCh := make(chan string, 1)
		go func() {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			ansCh <- strings.ToLower(strings.TrimSpace(line))
		}()
		select {
		case ans := <-ansCh:
			choice := config.DefaultTransport
			if ans == "y" || ans == "yes" {
				choice = res.Transport
			}
			if err := config.Set(config.GlobalPath(), "transport", choice); err != nil {
				fmt.Fprintf(os.Stderr, "localhoist: could not save config: %v\n", err)
			} else if choice == res.Transport {
				fmt.Printf("  ✔ saved — %s is now your default (%s)\n", choice, config.GlobalPath())
			} else {
				fmt.Printf("  ✔ keeping %s (saved to %s — you won't be asked again)\n", choice, config.GlobalPath())
			}
			fmt.Println()
		case <-sigCh:
			fmt.Println()
			return nil // deferred cleanup restores .env and stops the tunnel
		case <-tun.Done:
			cleanup()
			return fmt.Errorf("tunnel process exited unexpectedly: %v", tun.Err)
		}
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

func transportLabel(transport string) string {
	if transport == config.TransportCloudflare {
		return "Cloudflare quick"
	}
	return transport
}

func stdinIsTTY() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}
