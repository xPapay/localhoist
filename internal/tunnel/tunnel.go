// Package tunnel opens the public tunnel. Two transports, both BYO binary:
// Cloudflare quick tunnels (default — free, no account) and ngrok (stable
// custom domains). Each shells out to the user's binary and reads the public
// URL from its log stream — no dependency on any local API port being free.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	TransportCloudflare = "cloudflare"
	TransportNgrok      = "ngrok"
)

type Tunnel struct {
	// URL is the public https URL of the tunnel.
	URL string
	cmd *exec.Cmd
	// Done closes when the tunnel process exits.
	Done chan struct{}
	// Err holds the process exit error (if any) once Done is closed.
	Err error
}

// Start opens a tunnel to 127.0.0.1:port using the given transport.
func Start(ctx context.Context, transport string, port int, domain string) (*Tunnel, error) {
	switch transport {
	case TransportCloudflare:
		return StartCloudflared(ctx, port)
	case TransportNgrok:
		return StartNgrok(ctx, port, domain)
	default:
		return nil, fmt.Errorf("unknown transport %q", transport)
	}
}

// lineParser extracts either a tunnel URL or an error message from one line
// of a transport's log output; both empty means the line is noise.
type lineParser func(line []byte) (url, errMsg string)

type procConfig struct {
	name    string // binary name, used in error messages
	timeout time.Duration
	// abortOnErr fails fast on the first error line (ngrok errors are
	// fatal — bad authtoken, taken domain). When false, error lines are
	// only recorded and surfaced if the tunnel never comes up (cloudflared
	// logs transient errors it then retries past).
	abortOnErr bool
	// exitHint is appended when the process dies without an error line.
	exitHint string
}

// start launches the transport binary and waits until the tunnel URL is known.
func start(ctx context.Context, bin string, args []string, parse lineParser, pc procConfig) (*Tunnel, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	// Own process group: Ctrl+C in the terminal must reach only us, so
	// cleanup (restore .env, then stop the tunnel) runs in order.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // both transports log to a single stream anyway
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	t := &Tunnel{cmd: cmd, Done: make(chan struct{})}
	urlCh := make(chan string, 1)
	errCh := make(chan string, 1)
	var mu sync.Mutex
	var lastErr string

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			u, errMsg := parse(scanner.Bytes())
			if u != "" {
				select {
				case urlCh <- u:
				default:
				}
			} else if errMsg != "" {
				mu.Lock()
				lastErr = errMsg
				mu.Unlock()
				if pc.abortOnErr {
					select {
					case errCh <- errMsg:
					default:
					}
				}
			}
		}
	}()
	go func() {
		t.Err = cmd.Wait()
		close(t.Done)
	}()

	lastError := func() string {
		mu.Lock()
		defer mu.Unlock()
		return lastErr
	}

	select {
	case u := <-urlCh:
		t.URL = u
		return t, nil
	case msg := <-errCh:
		t.Stop()
		return nil, fmt.Errorf("%s: %s", pc.name, msg)
	case <-t.Done:
		if msg := lastError(); msg != "" {
			return nil, fmt.Errorf("%s exited before the tunnel came up: %s", pc.name, msg)
		}
		return nil, fmt.Errorf("%s exited before the tunnel came up %s", pc.name, pc.exitHint)
	case <-time.After(pc.timeout):
		t.Stop()
		if msg := lastError(); msg != "" {
			return nil, fmt.Errorf("timed out waiting for %s to open the tunnel (last error: %s)", pc.name, msg)
		}
		return nil, fmt.Errorf("timed out waiting for %s to open the tunnel", pc.name)
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	}
}

// Stop terminates the tunnel process gracefully, escalating to SIGKILL.
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
