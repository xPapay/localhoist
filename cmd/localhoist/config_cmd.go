package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xPapay/localhoist/internal/config"
)

const configUsage = `Usage:
  localhoist config                          show the effective config and where it comes from
  localhoist config set <key> <value>        set a key (global by default)
  localhoist config set --project <key> <value>
  localhoist config unset <key>              remove a key (global by default)
  localhoist config unset --project <key>

Keys:
  transport    cloudflare (quick tunnel, no account — the default) or ngrok
`

// runConfig handles the `localhoist config` subcommand.
func runConfig(args []string) error {
	if len(args) == 0 {
		return showConfig(".")
	}

	switch args[0] {
	case "set", "unset":
		fs := flag.NewFlagSet("config "+args[0], flag.ExitOnError)
		project := fs.Bool("project", false, "write the project config (./.localhoist.json) instead of the global one")
		fs.Usage = func() { fmt.Fprint(os.Stderr, configUsage) }
		fs.Parse(args[1:])
		rest := fs.Args()

		path := config.GlobalPath()
		if *project {
			path = config.ProjectPath(".")
		}

		if args[0] == "set" {
			if len(rest) != 2 {
				return fmt.Errorf("usage: localhoist config set [--project] <key> <value>")
			}
			if err := config.Set(path, rest[0], rest[1]); err != nil {
				return err
			}
			fmt.Printf("  ✔ %s = %s  (%s)\n", rest[0], rest[1], path)
			return nil
		}
		if len(rest) != 1 {
			return fmt.Errorf("usage: localhoist config unset [--project] <key>")
		}
		if err := config.Unset(path, rest[0]); err != nil {
			return err
		}
		fmt.Printf("  ✔ %s unset  (%s)\n", rest[0], path)
		return nil

	case "-h", "--help", "help":
		fmt.Print(configUsage)
		return nil

	default:
		return fmt.Errorf("unknown config command %q\n\n%s", args[0], configUsage)
	}
}

// showConfig prints the effective settings plus each layer's contribution,
// so "why is it using ngrok?" is answerable at a glance.
func showConfig(dir string) error {
	res, err := config.Resolve(dir, "")
	if err != nil {
		return err
	}

	fmt.Printf("  transport = %s  (%s)\n\n", res.Transport, sourceLabel(res))

	for _, layer := range []struct {
		name string
		path string
	}{
		{"global ", config.GlobalPath()},
		{"project", config.ProjectPath(dir)},
	} {
		m, err := config.Read(layer.path)
		if err != nil {
			return err
		}
		if v, ok := m["transport"].(string); ok {
			fmt.Printf("  %s  %s  transport=%s\n", layer.name, layer.path, v)
		} else {
			fmt.Printf("  %s  %s  (not set)\n", layer.name, layer.path)
		}
	}

	fmt.Println()
	fmt.Println("  change:  localhoist config set transport ngrok")
	fmt.Println("  revert:  localhoist config unset transport")
	return nil
}

func sourceLabel(res config.Resolution) string {
	if res.SourcePath != "" {
		return fmt.Sprintf("%s: %s", res.Source, res.SourcePath)
	}
	return res.Source.String()
}
