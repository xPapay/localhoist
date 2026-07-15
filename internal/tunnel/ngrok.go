package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// StartNgrok launches `ngrok http <port>` (with --domain when a static domain
// is configured) and waits until the tunnel URL is known. The URL comes from
// ngrok's JSON log stream — no dependency on its local API port being free.
func StartNgrok(ctx context.Context, port int, domain string) (*Tunnel, error) {
	bin, err := ensureBinary("ngrok", "ngrok",
		"install it:\n      brew install ngrok\n      or https://ngrok.com/download\n  (or use the default transport: localhoist --transport cloudflare)")
	if err != nil {
		return nil, err
	}

	args := []string{"http", "--log", "stdout", "--log-format", "json"}
	if domain != "" {
		args = append(args, "--domain="+domain)
	}
	args = append(args, "127.0.0.1:"+strconv.Itoa(port))

	t, err := start(ctx, bin, args, ParseLogLine, procConfig{
		name:       "ngrok",
		timeout:    20 * time.Second,
		abortOnErr: true, // ngrok's error lines are fatal: bad authtoken, taken domain, …
		exitHint:   fmt.Sprintf("(run `ngrok http %d` manually to see why)", port),
	})
	if err != nil && needsAuthGuidance(err) {
		return nil, fmt.Errorf("%w\n\n  ngrok needs a (free) account:\n    1. sign up:  https://dashboard.ngrok.com/signup\n    2. then run: ngrok config add-authtoken <your-token>", err)
	}
	return t, err
}

// needsAuthGuidance recognizes the unauthenticated-agent failure, the first
// thing every fresh ngrok install hits (ERR_NGROK_4018 and friends).
func needsAuthGuidance(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "authtoken") ||
		strings.Contains(msg, "err_ngrok_4018") ||
		strings.Contains(msg, "verified account")
}

// ParseLogLine extracts either a started-tunnel URL or an error message from
// one line of ngrok's JSON log output. Non-JSON lines are ignored.
func ParseLogLine(line []byte) (url string, errMsg string) {
	var entry map[string]any
	if err := json.Unmarshal(line, &entry); err != nil {
		return "", ""
	}
	if msg, _ := entry["msg"].(string); msg == "started tunnel" {
		url, _ = entry["url"].(string)
		return url, ""
	}
	// ngrok spells error level "eror"; also treat "crit" as fatal.
	if lvl, _ := entry["lvl"].(string); lvl == "eror" || lvl == "crit" {
		if e, ok := entry["err"].(string); ok && e != "" && e != "<nil>" {
			return "", e
		}
		if m, ok := entry["msg"].(string); ok {
			return "", m
		}
	}
	return "", ""
}
