package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupLayers points the global config at a temp dir and returns a project dir.
func setupLayers(t *testing.T) (globalPath, projectDir string) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return filepath.Join(xdg, "localhoist", "config.json"), t.TempDir()
}

func TestGlobalPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := GlobalPath(), filepath.Join("/xdg", "localhoist", "config.json"); got != want {
		t.Errorf("GlobalPath() = %q, want %q", got, want)
	}
}

func TestResolvePrecedence(t *testing.T) {
	global, proj := setupLayers(t)

	// Nothing configured → built-in default.
	res, err := Resolve(proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Transport != TransportCloudflare || res.Source != SourceDefault || res.Saved != "" {
		t.Errorf("default: got %+v", res)
	}

	// Global layer.
	if err := Set(global, "transport", TransportNgrok); err != nil {
		t.Fatal(err)
	}
	res, _ = Resolve(proj, "")
	if res.Transport != TransportNgrok || res.Source != SourceGlobal || res.Saved != TransportNgrok {
		t.Errorf("global: got %+v", res)
	}

	// Project overrides global.
	if err := Set(ProjectPath(proj), "transport", TransportCloudflare); err != nil {
		t.Fatal(err)
	}
	res, _ = Resolve(proj, "")
	if res.Transport != TransportCloudflare || res.Source != SourceProject {
		t.Errorf("project: got %+v", res)
	}

	// Flag overrides everything, but Saved still reports the file value.
	res, _ = Resolve(proj, TransportNgrok)
	if res.Transport != TransportNgrok || res.Source != SourceFlag || res.Saved != TransportCloudflare {
		t.Errorf("flag: got %+v", res)
	}

	// Invalid flag value errors.
	if _, err := Resolve(proj, "carrier-pigeon"); err == nil {
		t.Error("invalid flag transport: want error, got nil")
	}
}

func TestResolveRejectsInvalidFileValue(t *testing.T) {
	_, proj := setupLayers(t)
	if err := os.WriteFile(ProjectPath(proj), []byte(`{"transport": "warp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(proj, "")
	if err == nil || !strings.Contains(err.Error(), "warp") {
		t.Errorf("want invalid-transport error naming the value, got %v", err)
	}
}

func TestSetUnsetPreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"future_key": 42}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Set(path, "transport", TransportNgrok); err != nil {
		t.Fatal(err)
	}
	m, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if m["transport"] != TransportNgrok || m["future_key"] == nil {
		t.Errorf("after Set: %v", m)
	}

	if err := Unset(path, "transport"); err != nil {
		t.Fatal(err)
	}
	m, _ = Read(path)
	if _, ok := m["transport"]; ok {
		t.Error("transport still set after Unset")
	}
	if m["future_key"] == nil {
		t.Error("unknown key lost on Unset")
	}

	// Unset on a missing file is a no-op, not an error.
	if err := Unset(filepath.Join(dir, "nope.json"), "transport"); err != nil {
		t.Errorf("Unset on missing file: %v", err)
	}
}

func TestSetRejectsUnknownKeyAndValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Set(path, "colour", "green"); err == nil {
		t.Error("unknown key: want error")
	}
	if err := Set(path, "transport", "warp"); err == nil {
		t.Error("invalid transport: want error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("rejected Set must not create the file")
	}
}

func TestFinalizeDomainRule(t *testing.T) {
	// No domain → untouched.
	res, note, err := Finalize(Resolution{Transport: TransportCloudflare, Source: SourceDefault}, "")
	if err != nil || note != "" || res.Transport != TransportCloudflare {
		t.Errorf("no domain: got (%+v, %q, %v)", res, note, err)
	}

	// Domain + implicit default → silently upgraded to ngrok, with a note.
	res, note, err = Finalize(Resolution{Transport: TransportCloudflare, Source: SourceDefault}, "app.ngrok-free.dev")
	if err != nil || res.Transport != TransportNgrok || note == "" {
		t.Errorf("implicit default: got (%+v, %q, %v)", res, note, err)
	}

	// Domain + explicit cloudflare (flag or file) → conflict error.
	for _, src := range []Source{SourceFlag, SourceGlobal, SourceProject} {
		if _, _, err := Finalize(Resolution{Transport: TransportCloudflare, Source: src}, "app.ngrok-free.dev"); err == nil {
			t.Errorf("explicit cloudflare from %v + domain: want error", src)
		}
	}

	// Domain + ngrok → fine.
	if _, _, err := Finalize(Resolution{Transport: TransportNgrok, Source: SourceGlobal}, "app.ngrok-free.dev"); err != nil {
		t.Errorf("ngrok + domain: %v", err)
	}
}
