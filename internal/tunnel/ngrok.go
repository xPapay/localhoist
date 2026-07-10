// Package tunnel manages the BYO transport. v1 shells out to the user's
// ngrok binary and reads the public URL from its JSON log stream — no
// dependency on the ngrok local API port being free.
package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

type Tunnel struct {
	// URL is the public https URL of the tunnel.
	URL string
	cmd *exec.Cmd
	// Done closes when the ngrok process exits.
	Done chan struct{}
	// Err holds the process exit error (if any) once Done is closed.
	Err error
}

// StartNgrok launches `ngrok http <port>` (with --domain when a static domain
// is configured) and waits until the tunnel URL is known.
func StartNgrok(ctx context.Context, port int, domain string) (*Tunnel, error) {
	bin, err := exec.LookPath("ngrok")
	if err != nil {
		return nil, fmt.Errorf("ngrok not found in PATH — install it from https://ngrok.com/download (transport is BYO in this version)")
	}

	args := []string{"http", "--log", "stdout", "--log-format", "json"}
	if domain != "" {
		args = append(args, "--domain="+domain)
	}
	args = append(args, "127.0.0.1:"+strconv.Itoa(port))

	cmd := exec.CommandContext(ctx, bin, args...)
	// Own process group: Ctrl+C in the terminal must reach only us, so
	// cleanup (restore .env, then stop the tunnel) runs in order.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // ngrok logs everything to the same stream anyway
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	t := &Tunnel{cmd: cmd, Done: make(chan struct{})}
	urlCh := make(chan string, 1)
	errCh := make(chan string, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			if u, errMsg := ParseLogLine(scanner.Bytes()); u != "" {
				select {
				case urlCh <- u:
				default:
				}
			} else if errMsg != "" {
				select {
				case errCh <- errMsg:
				default:
				}
			}
		}
	}()
	go func() {
		t.Err = cmd.Wait()
		close(t.Done)
	}()

	select {
	case u := <-urlCh:
		t.URL = u
		return t, nil
	case msg := <-errCh:
		t.Stop()
		return nil, fmt.Errorf("ngrok: %s", msg)
	case <-t.Done:
		return nil, fmt.Errorf("ngrok exited before the tunnel came up (run `ngrok http %d` manually to see why)", port)
	case <-time.After(20 * time.Second):
		t.Stop()
		return nil, fmt.Errorf("timed out waiting for ngrok to open the tunnel")
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	}
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

// Stop terminates the ngrok process gracefully, escalating to SIGKILL.
func (t *Tunnel) Stop() {
	if t.cmd.Process == nil {
		return
	}
	t.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-t.Done:
	case <-time.After(3 * time.Second):
		t.cmd.Process.Kill()
		<-t.Done
	}
}
