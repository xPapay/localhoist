package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `APP_NAME=Personeo
APP_URL=http://localhost:8088

# comment stays put
REVERB_HOST="localhost"
REVERB_PORT=8080
REVERB_SCHEME=http
VITE_REVERB_HOST="${REVERB_HOST}"
`

func writeSample(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(sample), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGetStripsQuotes(t *testing.T) {
	f, err := Load(writeSample(t))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"APP_URL":     "http://localhost:8088",
		"REVERB_HOST": "localhost", // double-quoted in file
	} {
		got, ok := f.Get(key)
		if !ok || got != want {
			t.Errorf("Get(%s) = %q, %v; want %q, true", key, got, ok, want)
		}
	}
	if _, ok := f.Get("MISSING"); ok {
		t.Error("Get(MISSING) reported existing")
	}
}

func TestSetAndSavePreservesLayout(t *testing.T) {
	path := writeSample(t)
	f, _ := Load(path)

	if !f.Set("APP_URL", "https://tunnel.example") {
		t.Fatal("Set(APP_URL) said key missing")
	}
	if f.Set("NOT_THERE", "x") {
		t.Fatal("Set on a missing key must not report success (and never append)")
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "APP_URL=https://tunnel.example\n") {
		t.Errorf("patched value missing:\n%s", got)
	}
	if !strings.Contains(got, "# comment stays put\n") {
		t.Errorf("layout not preserved:\n%s", got)
	}
	if strings.Contains(got, "NOT_THERE") {
		t.Errorf("missing key was appended:\n%s", got)
	}
	// Only the one line changed.
	if strings.Count(got, "\n") != strings.Count(sample, "\n") {
		t.Errorf("line count changed:\n%s", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := writeSample(t)
	f, _ := Load(path)

	st, err := SaveState(f, []string{"APP_URL", "REVERB_HOST", "NOT_THERE"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Original["NOT_THERE"]; ok {
		t.Error("state must only snapshot keys that exist")
	}

	// Simulate the patch…
	f.Set("APP_URL", "https://tunnel.example")
	f.Set("REVERB_HOST", "tunnel.example")
	f.Save()

	// …then a crash + recovery via the sidecar file.
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("state file not found after SaveState")
	}
	if err := loaded.Restore(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != sample {
		t.Errorf("restore is not byte-exact (quotes must survive):\n%s", string(data))
	}
	if st, _ := LoadState(path); st != nil {
		t.Error("state file should be removed after Restore")
	}
}

func TestLoadStateAbsent(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), ".env"))
	if err != nil || st != nil {
		t.Errorf("expected nil, nil for absent state; got %v, %v", st, err)
	}
}
