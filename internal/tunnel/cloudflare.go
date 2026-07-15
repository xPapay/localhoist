package tunnel

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// quickTunnelURL matches the public URL cloudflared prints in its startup
// banner. Matching the URL anywhere keeps us independent of the banner's
// box-drawing layout, which has changed between cloudflared releases.
var quickTunnelURL = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// StartCloudflared launches a Cloudflare quick tunnel (`cloudflared tunnel
// --url …`) and waits until the public URL is known. Quick tunnels need no
// account — that's what makes them the default transport. Custom domains
// need ngrok; config.Finalize routes those runs there before we get here.
func StartCloudflared(ctx context.Context, port int) (*Tunnel, error) {
	bin, err := ensureBinary("cloudflared", "cloudflared",
		"install it:\n      brew install cloudflared\n      or https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/\n  (or use ngrok instead: localhoist --transport ngrok)")
	if err != nil {
		return nil, err
	}

	target := "http://127.0.0.1:" + strconv.Itoa(port)
	args := []string{"tunnel", "--url", target, "--no-autoupdate"}

	return start(ctx, bin, args, ParseCloudflaredLine, procConfig{
		name: "cloudflared",
		// Quick-tunnel provisioning is usually a couple of seconds, but the
		// edge handshake can be slow on a cold start — give it more room
		// than ngrok's session dial.
		timeout: 30 * time.Second,
		// cloudflared logs transient errors it then retries past (edge
		// reconnects), so error lines are recorded, not fatal.
		abortOnErr: false,
		exitHint:   fmt.Sprintf("(run `cloudflared tunnel --url %s` manually to see why)", target),
	})
}

// ParseCloudflaredLine extracts either the quick-tunnel URL or an error
// message from one line of cloudflared's console log output.
func ParseCloudflaredLine(line []byte) (url, errMsg string) {
	if m := quickTunnelURL.Find(line); m != nil {
		return string(m), ""
	}
	// Console format: "2026-07-15T10:00:00Z ERR message key=value …".
	if s := string(line); strings.Contains(s, " ERR ") {
		return "", strings.TrimSpace(s[strings.Index(s, " ERR ")+len(" ERR "):])
	}
	return "", ""
}
