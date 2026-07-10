// Package proxy implements the smart local mux: a single listener that
// routes app traffic, Vite dev-server assets/HMR, and Reverb websockets to
// their local upstreams — the job nginx did in the hand-rolled setup — and
// rewrites responses in-flight so no Vite restart or config is needed.
package proxy

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/xPapay/localhoist/internal/rewrite"
)

// Route sends requests whose path matches Prefix to Target. A Prefix ending
// in "/" matches that subtree plus the bare path itself ("/app/" matches
// "/app" and "/app/x", but not "/apple" — deliberately stricter than the
// nginx prefix locations this replaces).
type Route struct {
	Prefix string
	Target *url.URL
	Name   string
}

// vitePrefixes are the paths the Vite dev server owns: the set from the
// proven sail-proxy.conf.template, plus /__laravel_vite_plugin__/ which
// laravel-vite-plugin v3 uses to serve fonts from the dev server. HMR
// websockets are additionally routed by their `vite-hmr` subprotocol
// whatever their path, so no hmr.path config is needed.
var vitePrefixes = []string{
	"/@vite/", "/@id/", "/@fs/",
	"/resources/", "/node_modules/", "/vendor/",
	"/__vite_hmr",
	"/__laravel_vite_plugin__/",
}

// Config describes the upstreams and rewrite inputs for a project.
type Config struct {
	App    *url.URL // the Laravel app
	Vite   *url.URL // the Vite dev server
	Reverb *url.URL // the Reverb websocket server; nil when unused

	// HotFile is the project's public/hot path. When the file exists (Vite
	// dev server running), its origin is rewritten to the tunnel origin in
	// HTML responses, so @vite script tags work with no restart.
	HotFile string

	Logf func(format string, args ...any)
}

// Routes builds the path-routing table (also shown in the CLI banner).
func (c Config) Routes() []Route {
	var routes []Route
	if c.Reverb != nil {
		routes = append(routes, Route{Prefix: "/app/", Target: c.Reverb, Name: "reverb"})
	}
	for _, p := range vitePrefixes {
		routes = append(routes, Route{Prefix: p, Target: c.Vite, Name: "vite"})
	}
	routes = append(routes, Route{Prefix: "/", Target: c.App, Name: "app"})
	return routes
}

// Mux is an http.Handler dispatching to per-upstream reverse proxies.
type Mux struct {
	cfg     Config
	routes  []Route
	proxies map[string]*httputil.ReverseProxy // keyed by route name
	vite    Route
}

func NewMux(cfg Config) *Mux {
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	m := &Mux{cfg: cfg, routes: cfg.Routes(), proxies: map[string]*httputil.ReverseProxy{}}
	for _, r := range m.routes {
		if _, ok := m.proxies[r.Name]; !ok {
			m.proxies[r.Name] = m.newProxy(r.Name, r.Target)
		}
		if r.Name == "vite" {
			m.vite = r
		}
	}
	return m
}

func (m *Mux) newProxy(role string, target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Laravel and Reverb should see the public (tunnel) host the
			// browser used. Vite gets its own local host instead: it never
			// needs the public one, and Vite 6+ rejects unknown hosts
			// (server.allowedHosts) — localhost/IPs always pass.
			if role != "vite" {
				r.Out.Host = r.In.Host
			}

			// ngrok already sets X-Forwarded-For/Proto at the edge; pass
			// them through so Laravel knows the request was HTTPS. Fall
			// back to sane values for direct local hits. X-Forwarded-Host
			// always carries the public host — response rewriting reads it
			// back to learn the tunnel origin.
			r.SetXForwarded()
			if v := r.In.Header.Get("X-Forwarded-Proto"); v != "" {
				r.Out.Header.Set("X-Forwarded-Proto", v)
			}
			if v := r.In.Header.Get("X-Forwarded-For"); v != "" {
				r.Out.Header.Set("X-Forwarded-For", v)
			}
			r.Out.Header.Set("X-Forwarded-Host", r.In.Host)

			// Marker for the localhoist/laravel middleware: combined with
			// a loopback REMOTE_ADDR it tells Laravel this proxy may be
			// trusted, which replaces the APP_URL .env patch.
			r.Out.Header.Set("X-Localhoist", "1")

			// Responses we may rewrite must arrive uncompressed.
			if m.mayRewrite(role, r.Out) {
				r.Out.Header.Set("Accept-Encoding", "identity")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			m.rewriteResponse(role, resp)
			return nil
		},
		// Dev servers stream (HMR pushes, SSE); never buffer.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			m.cfg.Logf("upstream %s unreachable for %s %s: %v", target, r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("localhoist: upstream " + target.String() + " unreachable — is it running?\n"))
		},
	}
}

// mayRewrite reports whether a response to this outbound request is a
// candidate for body rewriting.
func (m *Mux) mayRewrite(role string, out *http.Request) bool {
	switch role {
	case "app":
		// Pages only — Blade output with @vite script tags.
		return strings.Contains(out.Header.Get("Accept"), "text/html")
	case "vite":
		// /@vite/client (HMR config) and any JS module (import.meta.env
		// with Reverb config). Cheap to be broad: Vite doesn't compress
		// dev responses anyway.
		return true
	}
	return false
}

// rewriteResponse applies the in-flight fixes. Any failure leaves the
// response untouched — rewriting is best-effort by design.
func (m *Mux) rewriteResponse(role string, resp *http.Response) {
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return
	}
	contentType := resp.Header.Get("Content-Type")

	switch {
	case role == "vite" && resp.Request.URL.Path == "/@vite/client":
		m.swapBody(resp, func(body []byte) ([]byte, bool) {
			out, recognized := rewrite.ViteClient(body)
			if !recognized {
				m.cfg.Logf("/@vite/client has an unrecognized shape — HMR may need vite.config for this Vite version")
			}
			return out, recognized
		})

	case role == "vite" && m.cfg.Reverb != nil && strings.Contains(contentType, "javascript"):
		host := publicHostname(resp.Request)
		if host == "" {
			return
		}
		m.swapBody(resp, func(body []byte) ([]byte, bool) {
			return rewrite.ReverbEnv(body, host)
		})

	case role == "app" && strings.Contains(contentType, "text/html"):
		hotOrigin := m.hotOrigin()
		origin := publicOrigin(resp.Request)
		if hotOrigin == "" || origin == "" {
			return
		}
		m.swapBody(resp, func(body []byte) ([]byte, bool) {
			return rewrite.HTML(body, hotOrigin, origin)
		})
	}
}

// swapBody buffers the response body through fn and fixes Content-Length.
func (m *Mux) swapBody(resp *http.Response, fn func([]byte) ([]byte, bool)) {
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return
	}
	out, _ := fn(body)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
}

// hotOrigin reads the Vite dev-server origin from the project's hot file
// (what Laravel's @vite directive bakes into HTML). Empty when Vite isn't
// running.
func (m *Mux) hotOrigin() string {
	if m.cfg.HotFile == "" {
		return ""
	}
	data, err := os.ReadFile(m.cfg.HotFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// publicOrigin reconstructs the origin the browser used from the headers the
// Rewrite hook stamped on the outbound request.
func publicOrigin(out *http.Request) string {
	host := out.Header.Get("X-Forwarded-Host")
	if host == "" {
		return ""
	}
	scheme := out.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + host
}

// publicHostname is the bare public hostname, without port.
func publicHostname(out *http.Request) string {
	host := out.Header.Get("X-Forwarded-Host")
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// route picks the upstream: Vite HMR websockets by subprotocol first (they
// can arrive on any path — Vite infers it from where the page was served),
// then longest-listed path prefix.
func (m *Mux) route(r *http.Request) Route {
	if m.vite.Target != nil && isViteWebsocket(r) {
		return m.vite
	}
	for _, rt := range m.routes {
		if matches(r.URL.Path, rt.Prefix) {
			return rt
		}
	}
	return m.routes[len(m.routes)-1]
}

// isViteWebsocket detects Vite's HMR (and ping fallback) websocket upgrades
// by their subprotocol.
func isViteWebsocket(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	proto := r.Header.Get("Sec-WebSocket-Protocol")
	return strings.Contains(proto, "vite-hmr") || strings.Contains(proto, "vite-ping")
}

func matches(path, prefix string) bool {
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/")
	}
	return strings.HasPrefix(path, prefix)
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.proxies[m.route(r).Name].ServeHTTP(w, r)
}
