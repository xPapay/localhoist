package laravel

import (
	"os"
	"path/filepath"
	"testing"
)

func project(t *testing.T, env string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectSailStyleProject(t *testing.T) {
	dir := project(t, `APP_URL=http://localhost:8088
VITE_PORT=5199
REVERB_APP_KEY=abc
REVERB_SERVER_PORT=9090
NGROK_TUNNEL_URL=https://static.ngrok-free.dev
`)
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AppUpstream.String(); got != "http://localhost:8088" {
		t.Errorf("AppUpstream = %s", got)
	}
	if got := p.ViteUpstream.String(); got != "http://127.0.0.1:5199" {
		t.Errorf("ViteUpstream = %s", got)
	}
	if p.ReverbUpstream == nil || p.ReverbUpstream.String() != "http://127.0.0.1:9090" {
		t.Errorf("ReverbUpstream = %v", p.ReverbUpstream)
	}
	if p.StaticDomain != "static.ngrok-free.dev" {
		t.Errorf("StaticDomain = %q (scheme must be stripped)", p.StaticDomain)
	}
}

func TestDetectDefaults(t *testing.T) {
	dir := project(t, `APP_URL=http://myapp.test
`)
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AppUpstream.String(); got != "http://myapp.test" {
		t.Errorf("AppUpstream = %s (Herd/Valet style host must be kept)", got)
	}
	if got := p.ViteUpstream.String(); got != "http://127.0.0.1:5173" {
		t.Errorf("ViteUpstream default = %s", got)
	}
	if p.ReverbUpstream != nil {
		t.Errorf("project without reverb config got ReverbUpstream %v", p.ReverbUpstream)
	}
}

func TestAppPortFallbackForLocalhost(t *testing.T) {
	dir := project(t, `APP_URL=http://localhost
APP_PORT=8090
BROADCAST_CONNECTION=reverb
`)
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AppUpstream.String(); got != "http://localhost:8090" {
		t.Errorf("AppUpstream = %s, want APP_PORT applied to bare localhost", got)
	}
	if p.ReverbUpstream == nil {
		t.Error("BROADCAST_CONNECTION=reverb must enable the reverb route")
	}
}

func TestHTTPSAppURLDowngradedLocally(t *testing.T) {
	dir := project(t, `APP_URL=https://myapp.test
`)
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AppUpstream.Scheme; got != "http" {
		t.Errorf("local upstream scheme = %s, want http", got)
	}
}

func TestDetectRejectsNonLaravelDir(t *testing.T) {
	if _, err := Detect(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory without artisan")
	}
}
