package rewrite

import (
	"strings"
	"testing"
)

// Mirrors vite 5.4 dist/client/client.mjs after serve-time replacement with
// the Sail-docs config `server.hmr = { host: 'localhost' }` — the setup that
// breaks through a plain tunnel.
const clientSailConfig = `console.debug("[vite] connecting...");
const importMetaUrl = new URL(import.meta.url);
const serverHost = "localhost:5173/";
const socketProtocol = null || (importMetaUrl.protocol === "https:" ? "wss" : "ws");
const hmrPort = null;
const socketHost = ` + "`${\"localhost\" || importMetaUrl.hostname}:${hmrPort || importMetaUrl.port}${\"/\"}`" + `;
const directSocketHost = "localhost:5173/";
const base = "/" || "/";
const wsToken = "3ODY0NTk2Mjc1NgB";
`

func TestViteClientNeutralizesSailConfig(t *testing.T) {
	out, ok := ViteClient([]byte(clientSailConfig))
	if !ok {
		t.Fatal("client shape not recognized")
	}
	got := string(out)
	if !strings.Contains(got, "${null || importMetaUrl.hostname}") {
		t.Errorf("baked hostname not neutralized:\n%s", got)
	}
	if !strings.Contains(got, `const wsToken = "3ODY0NTk2Mjc1NgB";`) {
		t.Errorf("wsToken must be preserved:\n%s", got)
	}
	if !strings.Contains(got, `const serverHost = "localhost:5173/";`) {
		t.Errorf("serverHost (diagnostics only) should be untouched:\n%s", got)
	}
}

// Fully-custom hmr config: explicit protocol, clientPort, host, and path.
func TestViteClientNeutralizesCustomConfig(t *testing.T) {
	in := `const socketProtocol = "ws" || (importMetaUrl.protocol === "https:" ? "wss" : "ws");
const hmrPort = 5174;
const socketHost = ` + "`${\"myhost.test\" || importMetaUrl.hostname}:${hmrPort || importMetaUrl.port}${\"/__vite_hmr\"}`" + `;
`
	out, ok := ViteClient([]byte(in))
	if !ok {
		t.Fatal("client shape not recognized")
	}
	got := string(out)
	for _, want := range []string{
		`const socketProtocol = null || (importMetaUrl.protocol === "https:" ? "wss" : "ws");`,
		`const hmrPort = null;`,
		"${null || importMetaUrl.hostname}",
		`${"/__vite_hmr"}`, // the hmr path must survive — the mux routes it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "myhost.test") || strings.Contains(got, "5174") {
		t.Errorf("baked host/port literals survived:\n%s", got)
	}
}

// Vite 4 style: single quotes, port baked directly into the template, and
// the ternary base form.
func TestViteClientHandlesVite4Style(t *testing.T) {
	in := `const socketProtocol = 'wss' || (importMetaUrl.protocol === 'https:' ? 'wss' : 'ws');
const socketHost = ` + "`${'app.example.com' || importMetaUrl.hostname}:${'443' || importMetaUrl.port}${'/' === '/' ? '' : '/'}`" + `;
`
	out, ok := ViteClient([]byte(in))
	if !ok {
		t.Fatal("client shape not recognized")
	}
	got := string(out)
	if strings.Contains(got, "app.example.com") || strings.Contains(got, "'443'") {
		t.Errorf("baked literals survived:\n%s", got)
	}
}

// Default config (everything already null) is recognized, not flagged as an
// unknown shape.
func TestViteClientDefaultConfigRecognized(t *testing.T) {
	in := `const socketProtocol = null || (importMetaUrl.protocol === "https:" ? "wss" : "ws");
const hmrPort = null;
const socketHost = ` + "`${null || importMetaUrl.hostname}:${hmrPort || importMetaUrl.port}${\"/\"}`" + `;
`
	out, ok := ViteClient([]byte(in))
	if !ok {
		t.Fatal("already-null literals must count as recognized")
	}
	if string(out) != in {
		t.Errorf("default config should pass through unchanged:\n%s", out)
	}
}

func TestViteClientUnknownShape(t *testing.T) {
	if _, ok := ViteClient([]byte("export default {};")); ok {
		t.Error("unrelated JS must not be reported as recognized")
	}
}

// Mirrors what vite:import-analysis injects into modules referencing
// import.meta.env (serializeDefine output: sorted keys, `"k": v` pairs).
const echoModule = `import.meta.env = {"BASE_URL": "/", "DEV": true, "MODE": "development", "PROD": false, "SSR": false, "VITE_APP_NAME": "Personeo", "VITE_REVERB_APP_KEY": "secretkey1", "VITE_REVERB_HOST": "localhost", "VITE_REVERB_PORT": "8080", "VITE_REVERB_SCHEME": "http"};
window.Echo = new Echo({ wsHost: import.meta.env.VITE_REVERB_HOST });
`

func TestReverbEnv(t *testing.T) {
	out, changed := ReverbEnv([]byte(echoModule), "abc123.ngrok-free.app")
	if !changed {
		t.Fatal("expected a rewrite")
	}
	got := string(out)
	for _, want := range []string{
		`"VITE_REVERB_HOST": "abc123.ngrok-free.app"`,
		`"VITE_REVERB_PORT": "443"`,
		`"VITE_REVERB_SCHEME": "https"`,
		`"VITE_REVERB_APP_KEY": "secretkey1"`, // must NOT be touched
		`"VITE_APP_NAME": "Personeo"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestReverbEnvNoReverb(t *testing.T) {
	in := []byte(`import.meta.env = {"BASE_URL": "/", "DEV": true};`)
	out, changed := ReverbEnv(in, "x.ngrok.app")
	if changed || string(out) != string(in) {
		t.Error("modules without reverb env must pass through untouched")
	}
}

func TestHTML(t *testing.T) {
	body := `<html><head>
<script type="module" src="http://[::1]:5173/@vite/client"></script>
<script type="module" src="http://[::1]:5173/resources/js/app.js"></script>
</head><body>ok</body></html>`
	out, changed := HTML([]byte(body), "http://[::1]:5173", "https://abc123.ngrok-free.app")
	if !changed {
		t.Fatal("expected a rewrite")
	}
	got := string(out)
	if strings.Contains(got, "[::1]") {
		t.Errorf("hot origin survived:\n%s", got)
	}
	if !strings.Contains(got, `src="https://abc123.ngrok-free.app/@vite/client"`) {
		t.Errorf("tunnel origin missing:\n%s", got)
	}

	// No hot origin / same origin → untouched.
	if _, changed := HTML([]byte(body), "", "https://x"); changed {
		t.Error("empty hot origin must be a no-op")
	}
	if _, changed := HTML([]byte(body), "http://[::1]:5173", "http://[::1]:5173"); changed {
		t.Error("identical origins must be a no-op")
	}
}
