// Package proxy implements the smart local mux: a single listener that
// routes app traffic, Vite dev-server assets/HMR, and Reverb websockets to
// their local upstreams — the job nginx did in the hand-rolled setup.
package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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

// vitePrefixes are the paths the Vite dev server owns, taken from the proven
// sail-proxy.conf.template. "/__vite_hmr" is the HMR websocket path that
// vite.config.js must set (server.hmr.path) for tunneled HMR to work.
var vitePrefixes = []string{
	"/@vite/", "/@id/", "/@fs/",
	"/resources/", "/node_modules/", "/vendor/",
	"/__vite_hmr",
}

// Routes builds the route table for a Laravel project. reverb may be nil.
func Routes(app, vite, reverb *url.URL) []Route {
	var routes []Route
	if reverb != nil {
		routes = append(routes, Route{Prefix: "/app/", Target: reverb, Name: "reverb"})
	}
	for _, p := range vitePrefixes {
		routes = append(routes, Route{Prefix: p, Target: vite, Name: "vite"})
	}
	// Default route, matched last.
	routes = append(routes, Route{Prefix: "/", Target: app, Name: "app"})
	return routes
}

// Mux is an http.Handler that dispatches to per-upstream reverse proxies.
type Mux struct {
	routes  []Route
	proxies map[string]*httputil.ReverseProxy // keyed by target URL string
	logf    func(format string, args ...any)
}

func NewMux(routes []Route, logf func(format string, args ...any)) *Mux {
	if logf == nil {
		logf = log.Printf
	}
	m := &Mux{routes: routes, proxies: map[string]*httputil.ReverseProxy{}, logf: logf}
	for _, r := range routes {
		key := r.Target.String()
		if _, ok := m.proxies[key]; !ok {
			m.proxies[key] = m.newProxy(r.Target)
		}
	}
	return m
}

func (m *Mux) newProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Keep the public (tunnel) host so Laravel and Vite see the
			// URL the browser used — the nginx config's `proxy_set_header
			// Host $http_host`.
			r.Out.Host = r.In.Host

			// ngrok already sets X-Forwarded-For/Proto at the edge; pass
			// them through so Laravel knows the request was HTTPS. Fall
			// back to sane values for direct local hits.
			r.SetXForwarded()
			if v := r.In.Header.Get("X-Forwarded-Proto"); v != "" {
				r.Out.Header.Set("X-Forwarded-Proto", v)
			}
			if v := r.In.Header.Get("X-Forwarded-For"); v != "" {
				r.Out.Header.Set("X-Forwarded-For", v)
			}
			r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
		},
		// Dev servers stream (HMR pushes, SSE); never buffer.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			m.logf("upstream %s unreachable for %s %s: %v", target, r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("expose: upstream " + target.String() + " unreachable — is it running?\n"))
		},
	}
}

// match returns the first route whose prefix matches path.
func (m *Mux) match(path string) Route {
	for _, r := range m.routes {
		if matches(path, r.Prefix) {
			return r
		}
	}
	// Unreachable when routes ends with the "/" default, but keep a fallback.
	return m.routes[len(m.routes)-1]
}

func matches(path, prefix string) bool {
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/")
	}
	return strings.HasPrefix(path, prefix)
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := m.match(r.URL.Path)
	m.proxies[route.Target.String()].ServeHTTP(w, r)
}
