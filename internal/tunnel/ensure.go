package tunnel

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ensureBinary resolves the transport binary, and when it's missing offers
// to `brew install` it (only when brew exists and we're on a real terminal —
// never in CI or piped runs). Declining, or having no brew, falls through to
// a plain error carrying the install instructions.
func ensureBinary(name, brewFormula, hint string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	if stdioIsTTY() {
		if brew, err := exec.LookPath("brew"); err == nil {
			fmt.Printf("  %s isn't installed. Install it now with `brew install %s`? [Y/n] ", name, brewFormula)
			if readYes(true) {
				cmd := exec.Command(brew, "install", brewFormula)
				cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := cmd.Run(); err != nil {
					return "", fmt.Errorf("brew install %s failed: %w", brewFormula, err)
				}
				if path, err := exec.LookPath(name); err == nil {
					fmt.Println()
					return path, nil
				}
				return "", fmt.Errorf("%s still not in PATH after brew install", name)
			}
		}
	}

	return "", fmt.Errorf("%s not found in PATH — %s", name, hint)
}

// readYes reads one answer line from stdin; empty input means defaultYes.
func readYes(defaultYes bool) bool {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes
	case "y", "yes":
		return true
	default:
		return false
	}
}

func stdioIsTTY() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}
