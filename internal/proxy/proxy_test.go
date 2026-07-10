package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// upstream spins up a test server that reports which upstream answered and
// echoes the request details we care about.
func upstream(t *testing.T, name string) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", name)
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Seen-Fwd-Host", r.Header.Get("X-Forwarded-Host"))
		w.Header().Set("X-Seen-Marker", r.Header.Get("X-Localhoist"))
		io.WriteString(w, name+":"+r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func silentLogf(string, ...any) {}

func newTestMux(t *testing.T, withReverb bool) *Mux {
	cfg := Config{App: upstream(t, "app"), Vite: upstream(t, "vite"), Logf: silentLogf}
	if withReverb {
		cfg.Reverb = upstream(t, "reverb")
	}
	return NewMux(cfg)
}

func TestRouting(t *testing.T) {
	front := httptest.NewServer(newTestMux(t, true))
	defer front.Close()

	cases := map[string]string{
		"/":                     "app",
		"/dashboard":            "app",
		"/apple":                "app", // must NOT hit reverb's /app route
		"/application/settings": "app",
		"/app":                  "reverb",
		"/app/personeo-key":     "reverb",
		"/@vite/client":         "vite",
		"/@id/some-module":      "vite",
		"/@fs/Users/x/file.js":  "vite",
		"/resources/js/app.js":  "vite",
		"/node_modules/x.js":    "vite",
		"/vendor/pkg/asset.js":  "vite",
		"/__vite_hmr":           "vite",
	}
	for path, want := range cases {
		resp, err := http.Get(front.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("X-Upstream"); got != want {
			t.Errorf("%s routed to %q, want %q", path, got, want)
		}
	}
}

func TestRoutingWithoutReverb(t *testing.T) {
	front := httptest.NewServer(newTestMux(t, false))
	defer front.Close()

	resp, err := http.Get(front.URL + "/app/whatever")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-Upstream"); got != "app" {
		t.Errorf("/app without reverb routed to %q, want app", got)
	}
}

// Vite HMR websockets are routed by subprotocol whatever their path — with
// default Vite config the client connects on "/", which would otherwise hit
// the app (or Reverb, if the page lives under /app).
func TestViteWebsocketRoutedBySubprotocol(t *testing.T) {
	m := newTestMux(t, true)

	for _, path := range []string{"/", "/app/nested", "/dashboard"} {
		r := httptest.NewRequest("GET", path+"?token=abc", nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Sec-WebSocket-Protocol", "vite-hmr")
		if got := m.route(r).Name; got != "vite" {
			t.Errorf("hmr ws on %s routed to %q, want vite", path, got)
		}
	}

	// vite-ping (direct-connection fallback) too.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Protocol", "vite-ping")
	if got := m.route(r).Name; got != "vite" {
		t.Errorf("vite-ping ws routed to %q, want vite", got)
	}

	// A non-Vite websocket on / must still reach the app.
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Upgrade", "websocket")
	if got := m.route(r).Name; got != "app" {
		t.Errorf("plain ws on / routed to %q, want app", got)
	}

	// And Reverb's own websocket keeps its path route.
	r = httptest.NewRequest("GET", "/app/key123", nil)
	r.Header.Set("Upgrade", "websocket")
	if got := m.route(r).Name; got != "reverb" {
		t.Errorf("reverb ws routed to %q, want reverb", got)
	}
}

// Laravel and Reverb must see the PUBLIC host; Vite gets its own local host
// (Vite 6+ rejects unknown Host headers via server.allowedHosts).
func TestHostAndForwardedHeaders(t *testing.T) {
	m := newTestMux(t, false)
	front := httptest.NewServer(m)
	defer front.Close()

	get := func(path string) *http.Response {
		req, _ := http.NewRequest("GET", front.URL+path, nil)
		req.Host = "abc123.ngrok-free.app"
		req.Header.Set("X-Forwarded-Proto", "https")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	app := get("/dashboard")
	if got := app.Header.Get("X-Seen-Host"); got != "abc123.ngrok-free.app" {
		t.Errorf("app saw Host %q, want the tunnel host", got)
	}
	if got := app.Header.Get("X-Seen-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto was %q, want https passed through", got)
	}
	if got := app.Header.Get("X-Seen-Fwd-Host"); got != "abc123.ngrok-free.app" {
		t.Errorf("X-Forwarded-Host was %q, want the tunnel host", got)
	}
	if got := app.Header.Get("X-Seen-Marker"); got != "1" {
		t.Errorf("X-Localhoist marker was %q, want \"1\" (the middleware keys on it)", got)
	}

	vite := get("/@id/mod")
	if got := vite.Header.Get("X-Seen-Host"); got != m.cfg.Vite.Host {
		t.Errorf("vite saw Host %q, want its local host %q", got, m.cfg.Vite.Host)
	}
	if got := vite.Header.Get("X-Seen-Fwd-Host"); got != "abc123.ngrok-free.app" {
		t.Errorf("vite X-Forwarded-Host was %q, want the tunnel host", got)
	}
}

func TestViteClientRewrittenInFlight(t *testing.T) {
	viteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		io.WriteString(w, "const hmrPort = null;\nconst socketHost = `${\"localhost\" || importMetaUrl.hostname}:${hmrPort || importMetaUrl.port}${\"/\"}`;\n")
	}))
	defer viteSrv.Close()
	viteURL, _ := url.Parse(viteSrv.URL)

	m := NewMux(Config{App: upstream(t, "app"), Vite: viteURL, Logf: silentLogf})
	front := httptest.NewServer(m)
	defer front.Close()

	resp, err := http.Get(front.URL + "/@vite/client")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "${null || importMetaUrl.hostname}") {
		t.Errorf("baked hostname not neutralized in-flight:\n%s", body)
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q does not match rewritten body length %d", got, len(body))
	}
}

func TestReverbEnvRewrittenInFlight(t *testing.T) {
	viteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		io.WriteString(w, `import.meta.env = {"VITE_REVERB_HOST": "localhost", "VITE_REVERB_PORT": "8080", "VITE_REVERB_SCHEME": "http"};`)
	}))
	defer viteSrv.Close()
	viteURL, _ := url.Parse(viteSrv.URL)

	m := NewMux(Config{App: upstream(t, "app"), Vite: viteURL, Reverb: upstream(t, "reverb"), Logf: silentLogf})
	front := httptest.NewServer(m)
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/resources/js/echo.js", nil)
	req.Host = "abc123.ngrok-free.app"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	for _, want := range []string{
		`"VITE_REVERB_HOST": "abc123.ngrok-free.app"`,
		`"VITE_REVERB_PORT": "443"`,
		`"VITE_REVERB_SCHEME": "https"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestHTMLHotOriginRewrittenInFlight(t *testing.T) {
	appSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, `<script type="module" src="http://[::1]:5173/@vite/client"></script>`)
	}))
	defer appSrv.Close()
	appURL, _ := url.Parse(appSrv.URL)

	hotFile := filepath.Join(t.TempDir(), "hot")
	if err := os.WriteFile(hotFile, []byte("http://[::1]:5173"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewMux(Config{App: appURL, Vite: upstream(t, "vite"), HotFile: hotFile, Logf: silentLogf})
	front := httptest.NewServer(m)
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/", nil)
	req.Host = "abc123.ngrok-free.app"
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	want := `src="https://abc123.ngrok-free.app/@vite/client"`
	if !strings.Contains(string(body), want) {
		t.Errorf("hot origin not rewritten to tunnel origin:\n%s", body)
	}
}

func TestUnreachableUpstreamReturns502(t *testing.T) {
	dead, _ := url.Parse("http://127.0.0.1:1") // nothing listens here
	m := NewMux(Config{App: dead, Vite: upstream(t, "vite"), Logf: silentLogf})
	front := httptest.NewServer(m)
	defer front.Close()

	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}
