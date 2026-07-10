// Package envfile reads and patches dotenv files while preserving their
// layout, and keeps a sidecar state file so original values survive a crash.
package envfile

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// File is a dotenv file loaded into memory. Mutations happen in memory;
// Save writes the file back atomically.
type File struct {
	Path  string
	lines []string
}

func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSuffix(string(data), "\n")
	return &File{Path: path, lines: strings.Split(content, "\n")}, nil
}

// Get returns the value of the first `KEY=value` line, with surrounding
// quotes stripped, and whether the key exists at all.
func (f *File) Get(key string) (string, bool) {
	raw, ok := f.GetRaw(key)
	return unquote(raw), ok
}

// GetRaw returns the value exactly as written in the file, quotes and all.
// Restore paths must use this so the file round-trips byte-for-byte.
func (f *File) GetRaw(key string) (string, bool) {
	prefix := key + "="
	for _, line := range f.lines {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true
		}
	}
	return "", false
}

// Set replaces the first `KEY=...` line in memory and reports whether the
// key existed. Keys that don't exist are left alone — we never append.
func (f *File) Set(key, value string) bool {
	prefix := key + "="
	for i, line := range f.lines {
		if strings.HasPrefix(line, prefix) {
			f.lines[i] = prefix + value
			return true
		}
	}
	return false
}

// Save writes the file back atomically (temp file + rename), preserving the
// original file's permissions.
func (f *File) Save() error {
	perm := os.FileMode(0644)
	if info, err := os.Stat(f.Path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp := f.Path + ".expose-tmp"
	content := strings.Join(f.lines, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(content), perm); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// State records the original values of the keys we patched, so they can be
// restored on exit — or on the next start, if the previous run crashed.
// It lives next to the .env file and contains no secrets, only the handful
// of URL/host keys expose rewrites.
type State struct {
	EnvPath  string            `json:"env_path"`
	Original map[string]string `json:"original"`
}

func StatePath(envPath string) string {
	return envPath + ".expose-state.json"
}

// SaveState snapshots the current raw values of keys (those that exist) into
// the sidecar file. Call this BEFORE patching. Raw values (quotes included)
// make Restore byte-exact.
func SaveState(env *File, keys []string) (*State, error) {
	st := &State{EnvPath: env.Path, Original: map[string]string{}}
	for _, k := range keys {
		if v, ok := env.GetRaw(k); ok {
			st.Original[k] = v
		}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(StatePath(env.Path), data, 0600); err != nil {
		return nil, err
	}
	return st, nil
}

// LoadState returns the sidecar state if one exists (nil if not).
func LoadState(envPath string) (*State, error) {
	data, err := os.ReadFile(StatePath(envPath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("corrupt state file %s: %w", StatePath(envPath), err)
	}
	return &st, nil
}

// Restore writes the original values back into the env file and removes the
// sidecar state file.
func (st *State) Restore() error {
	env, err := Load(st.EnvPath)
	if err != nil {
		return err
	}
	for k, v := range st.Original {
		env.Set(k, v)
	}
	if err := env.Save(); err != nil {
		return err
	}
	return os.Remove(StatePath(st.EnvPath))
}
