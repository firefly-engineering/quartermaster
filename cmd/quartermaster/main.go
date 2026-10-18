// Command quartermaster keeps the day's worklog note and dispatches its work (docs/spec.md).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		if _, err := fmt.Fprintln(stdout, version); err != nil {
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintln(stderr, "usage: quartermaster version")
	return 2
}

// defaultConfigPath is the user file's default path (spec §3): $XDG_CONFIG_HOME, else
// ~/.config, joined with quartermaster/config.toml. A relative XDG_CONFIG_HOME is ignored, as
// the XDG spec requires. It returns "" when neither base is known.
func defaultConfigPath(getenv func(string) string, home string) string {
	base := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "quartermaster", "config.toml")
}
