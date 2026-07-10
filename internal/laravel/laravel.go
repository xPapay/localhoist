// Package laravel detects a Laravel project and derives the local upstreams
// (app, Vite dev server, Reverb) that the mux proxies to.
package laravel

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/xPapay/localhoist/internal/envfile"
)

type Project struct {
	Dir string
	Env *envfile.File

	// AppURL is the project's original APP_URL (pre-patch).
	AppURL string
	// AppUpstream is where the Laravel app itself is served locally,
	// derived from APP_URL (Sail: http://localhost:8088, Herd: http://app.test).
	AppUpstream *url.URL
	// ViteUpstream is the Vite dev server.
	ViteUpstream *url.URL
	// ReverbUpstream is the Reverb websocket server; nil if the project
	// doesn't use Reverb.
	ReverbUpstream *url.URL

	// StaticDomain is a stable tunnel domain (NGROK_TUNNEL_URL), if configured.
	StaticDomain string
	// ConfigCached reports whether bootstrap/cache/config.php exists, in
	// which case .env patches won't reach the app until config:clear.
	ConfigCached bool
}

// Detect loads the project at dir. It requires an `artisan` file and a `.env`.
func Detect(dir string) (*Project, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, "artisan")); err != nil {
		return nil, fmt.Errorf("no artisan file in %s — not a Laravel project (use --dir to point at one)", dir)
	}
	envPath := filepath.Join(dir, ".env")
	env, err := envfile.Load(envPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", envPath, err)
	}

	p := &Project{Dir: dir, Env: env}

	// OS environment takes precedence over .env, mirroring how Laravel and
	// the original bin/expose script resolve values.
	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		v, _ := env.Get(key)
		return v
	}

	p.AppURL = get("APP_URL")
	p.AppUpstream, err = deriveAppUpstream(p.AppURL, get("APP_PORT"))
	if err != nil {
		return nil, err
	}

	vitePort := intOr(get("VITE_PORT"), 5173)
	p.ViteUpstream = localURL(vitePort)

	if get("REVERB_APP_KEY") != "" || get("BROADCAST_CONNECTION") == "reverb" {
		p.ReverbUpstream = localURL(intOr(get("REVERB_SERVER_PORT"), 8080))
	}

	p.StaticDomain = stripScheme(get("NGROK_TUNNEL_URL"))

	if _, err := os.Stat(filepath.Join(dir, "bootstrap", "cache", "config.php")); err == nil {
		p.ConfigCached = true
	}

	return p, nil
}

// deriveAppUpstream turns APP_URL into the local address the app answers on.
// Port priority: explicit port in APP_URL, then APP_PORT (Sail's published
// port), then 80.
func deriveAppUpstream(appURL, appPort string) (*url.URL, error) {
	if appURL == "" {
		return localURL(intOr(appPort, 80)), nil
	}
	u, err := url.Parse(appURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("APP_URL %q is not a valid URL", appURL)
	}
	if u.Port() == "" && appPort != "" && isLocalHostname(u.Hostname()) {
		u.Host = u.Hostname() + ":" + appPort
	}
	// The tunnel terminates TLS; locally we always speak plain HTTP.
	u.Scheme = "http"
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return u, nil
}

func isLocalHostname(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "0.0.0.0"
}

func localURL(port int) *url.URL {
	return &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
}

func intOr(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return fallback
}

func stripScheme(s string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
	}
	return s
}
