// Package rewrite fixes dev-server responses in-flight so a Laravel app
// works through the tunnel with no Vite restart and no vite.config changes.
//
// Vite bakes connection config into what it serves at dev-server start:
// HMR host/port/protocol as JSON literals in /@vite/client, and the
// import.meta.env object (which Echo reads for Reverb) into every module
// that references it. Rewriting those literals as they pass through the
// mux is what makes "zero config, zero restart" possible.
//
// Shapes verified against vite 5.4 dist/client/client.mjs and the
// serializeDefine output of vite:import-analysis.
package rewrite

import (
	"bytes"
	"fmt"
	"regexp"
)

// The served /@vite/client contains (after Vite's serve-time replacement):
//
//	const socketProtocol = null || (importMetaUrl.protocol === "https:" ? "wss" : "ws");
//	const hmrPort = null;
//	const socketHost = `${"localhost" || importMetaUrl.hostname}:${hmrPort || importMetaUrl.port}${"/"}`;
//
// where the literals are whatever server.hmr config was at startup. Forcing
// them back to null makes the client infer everything from import.meta.url —
// which, through the tunnel, IS the tunnel origin. The `?token=` and the
// hmr path suffix are left untouched.
var viteClientLits = []*regexp.Regexp{
	regexp.MustCompile(`(const socketProtocol\s*=\s*)(null|"[^"]*"|'[^']*')(\s*\|\|)`),
	regexp.MustCompile(`(const hmrPort\s*=\s*)(null|\d+|"[^"]*"|'[^']*')(\s*;)`),
	regexp.MustCompile(`(\$\{)(null|"[^"]*"|'[^']*')(\s*\|\|\s*importMetaUrl\.hostname\})`),
	regexp.MustCompile(`(\$\{)(null|\d+|"[^"]*"|'[^']*')(\s*\|\|\s*importMetaUrl\.port\})`),
}

// ViteClient neutralizes the HMR literals baked into /@vite/client. The
// second return reports whether the client's shape was recognized — false
// on an unknown future layout, in which case the body passes through
// unchanged (already-null literals still count as recognized).
func ViteClient(body []byte) ([]byte, bool) {
	recognized := false
	for _, re := range viteClientLits {
		if re.Match(body) {
			recognized = true
			body = re.ReplaceAll(body, []byte("${1}null${3}"))
		}
	}
	return body, recognized
}

// Vite's import-analysis plugin injects, into every dev-served module that
// references import.meta.env:
//
//	import.meta.env = {"BASE_URL": "/", ..., "VITE_REVERB_HOST": "localhost",
//	  "VITE_REVERB_PORT": "8080", "VITE_REVERB_SCHEME": "http", ...};
//
// serialized ONCE at server start — so Echo keeps pointing at localhost
// until a restart. Rewriting the three values points it at the tunnel.
var reverbEnvKey = regexp.MustCompile(`("VITE_REVERB_(?:HOST|PORT|SCHEME)"\s*:\s*)"(?:[^"\\]|\\.)*"`)

// ReverbEnv points the baked Reverb client env at the tunnel host (bare
// hostname; the tunnel always terminates TLS on 443).
func ReverbEnv(body []byte, host string) ([]byte, bool) {
	if !bytes.Contains(body, []byte(`"VITE_REVERB_HOST"`)) {
		return body, false
	}
	values := map[string]string{
		"HOST":   host,
		"PORT":   "443",
		"SCHEME": "https",
	}
	out := reverbEnvKey.ReplaceAllFunc(body, func(m []byte) []byte {
		sub := reverbEnvKey.FindSubmatch(m)
		for suffix, v := range values {
			if bytes.Contains(sub[1], []byte("VITE_REVERB_"+suffix)) {
				return append(append([]byte{}, sub[1]...), fmt.Sprintf("%q", v)...)
			}
		}
		return m
	})
	return out, !bytes.Equal(out, body)
}

// HTML replaces the Vite dev-server origin that Laravel's @vite directive
// baked into the page (read from public/hot, e.g. "http://[::1]:5173") with
// the public tunnel origin, so every dev asset request flows through the mux.
func HTML(body []byte, hotOrigin, publicOrigin string) ([]byte, bool) {
	if hotOrigin == "" || hotOrigin == publicOrigin || !bytes.Contains(body, []byte(hotOrigin)) {
		return body, false
	}
	return bytes.ReplaceAll(body, []byte(hotOrigin), []byte(publicOrigin)), true
}
