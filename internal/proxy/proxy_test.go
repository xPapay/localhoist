package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		io.WriteString(w, name+":"+r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func newTestMux(t *testing.T, withReverb bool) http.Handler {
	app := upstream(t, "app")
	vite := upstream(t, "vite")
	var reverb *url.URL
	if withReverb {
		reverb = upstream(t, "reverb")
	}
	return NewMux(Routes(app, vite, reverb), func(string, ...any) {})
}

func TestRouting(t *testing.T) {
	mux := newTestMux(t, true)
	front := httptest.NewServer(mux)
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
	mux := newTestMux(t, false)
	front := httptest.NewServer(mux)
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

// The upstream must see the PUBLIC host (what the browser used), not the
// local upstream address — that's what keeps Laravel's URL generation and
// Vite's origin checks working through the tunnel.
func TestHostAndForwardedHeaders(t *testing.T) {
	mux := newTestMux(t, false)
	front := httptest.NewServer(mux)
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/dashboard", nil)
	req.Host = "abc123.ngrok-free.app"
	req.Header.Set("X-Forwarded-Proto", "https") // as ngrok sets at the edge

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := resp.Header.Get("X-Seen-Host"); got != "abc123.ngrok-free.app" {
		t.Errorf("upstream saw Host %q, want the tunnel host", got)
	}
	if got := resp.Header.Get("X-Seen-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto was %q, want https passed through from the edge", got)
	}
	if got := resp.Header.Get("X-Seen-Fwd-Host"); got != "abc123.ngrok-free.app" {
		t.Errorf("X-Forwarded-Host was %q, want the tunnel host", got)
	}
}

func TestUnreachableUpstreamReturns502(t *testing.T) {
	dead, _ := url.Parse("http://127.0.0.1:1") // nothing listens here
	vite := upstream(t, "vite")
	mux := NewMux(Routes(dead, vite, nil), func(string, ...any) {})
	front := httptest.NewServer(mux)
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
