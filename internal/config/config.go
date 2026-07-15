// Package config resolves localhoist settings from layered sources:
// --flag > project .localhoist.json > global config.json > built-in default.
// The transport default lives in the *global* config (it's a machine
// preference — my ngrok account is on my machine), with the project file
// available for repos that need a specific transport.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	TransportCloudflare = "cloudflare"
	TransportNgrok      = "ngrok"

	// DefaultTransport is the zero-config choice: Cloudflare quick tunnels
	// need no account, so the first run just works.
	DefaultTransport = TransportCloudflare
)

// knownKeys guards `config set` against typos; add new settings here.
var knownKeys = map[string]bool{"transport": true}

// Source says which layer supplied the effective value.
type Source int

const (
	SourceDefault Source = iota
	SourceGlobal
	SourceProject
	SourceFlag
)

func (s Source) String() string {
	switch s {
	case SourceGlobal:
		return "global config"
	case SourceProject:
		return "project config"
	case SourceFlag:
		return "--transport flag"
	default:
		return "default"
	}
}

// Resolution is the effective transport plus where it came from.
type Resolution struct {
	Transport  string
	Source     Source
	SourcePath string // config file path when Source is global/project
	// Saved is what the config files say, ignoring any flag — "" when
	// neither file sets a transport. Used to decide whether the user has
	// ever made an explicit choice (and so whether to offer saving one).
	Saved string
}

// GlobalPath returns the global config file, honoring XDG_CONFIG_HOME.
func GlobalPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "localhoist", "config.json")
}

// ProjectPath returns the per-project config file inside dir.
func ProjectPath(dir string) string {
	return filepath.Join(dir, ".localhoist.json")
}

// Resolve layers the config sources for dir and applies flagValue on top.
func Resolve(dir, flagValue string) (Resolution, error) {
	res := Resolution{Transport: DefaultTransport, Source: SourceDefault}

	for _, layer := range []struct {
		path string
		src  Source
	}{
		{GlobalPath(), SourceGlobal},
		{ProjectPath(dir), SourceProject},
	} {
		v, err := readTransport(layer.path)
		if err != nil {
			return res, err
		}
		if v != "" {
			res = Resolution{Transport: v, Source: layer.src, SourcePath: layer.path, Saved: v}
		}
	}

	if flagValue != "" {
		if err := ValidateTransport(flagValue); err != nil {
			return res, err
		}
		res = Resolution{Transport: flagValue, Source: SourceFlag, Saved: res.Saved}
	}
	return res, nil
}

// Finalize applies the static-domain rule: quick tunnels get random
// *.trycloudflare.com URLs, so a configured domain needs ngrok. When the
// transport was only the built-in default, upgrade silently (NGROK_TUNNEL_URL
// in .env is as explicit as it gets); when the user explicitly chose
// cloudflare, surface the conflict instead of ignoring half of it.
func Finalize(res Resolution, domain string) (Resolution, string, error) {
	if domain == "" || res.Transport == TransportNgrok {
		return res, "", nil
	}
	if res.Source == SourceDefault {
		res.Transport = TransportNgrok
		return res, "static domain " + domain + " → using ngrok", nil
	}
	return res, "", fmt.Errorf(
		"a static domain (%s) needs ngrok — Cloudflare quick tunnels use random *.trycloudflare.com URLs; rerun with --transport ngrok or drop the domain (--domain / NGROK_TUNNEL_URL)",
		domain)
}

// ValidateTransport rejects anything but the supported transports.
func ValidateTransport(v string) error {
	if v != TransportCloudflare && v != TransportNgrok {
		return fmt.Errorf("unknown transport %q (valid: %s, %s)", v, TransportCloudflare, TransportNgrok)
	}
	return nil
}

// Read loads a config file as a generic map so keys this version doesn't
// know about survive a read-modify-write. A missing file is an empty config.
func Read(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Set validates and writes key=value into the config file at path.
func Set(path, key, value string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if key == "transport" {
		if err := ValidateTransport(value); err != nil {
			return err
		}
	}
	m, err := Read(path)
	if err != nil {
		return err
	}
	m[key] = value
	return write(path, m)
}

// Unset removes key from the config file at path (a no-op if absent).
func Unset(path, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	m, err := Read(path)
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return write(path, m)
}

func validateKey(key string) error {
	if !knownKeys[key] {
		keys := make([]string, 0, len(knownKeys))
		for k := range knownKeys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("unknown config key %q (valid: %s)", key, strings.Join(keys, ", "))
	}
	return nil
}

func readTransport(path string) (string, error) {
	m, err := Read(path)
	if err != nil {
		return "", err
	}
	raw, ok := m["transport"]
	if !ok {
		return "", nil
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: transport must be a string", path)
	}
	if err := ValidateTransport(v); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}

func write(path string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
