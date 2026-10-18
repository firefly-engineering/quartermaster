// Command quartermaster keeps the day's worklog note and dispatches its work (docs/spec.md).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/firefly-engineering/quartermaster/internal/server"
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
	if len(args) == 1 && args[0] == "serve" {
		return serve(stderr)
	}
	_, _ = fmt.Fprintln(stderr, "usage: quartermaster version | serve")
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

// serve runs the MCP server on stdin and stdout until the client disconnects.
func serve(stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := server.New(version)
	if err == nil {
		err = s.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "quartermaster serve:", err)
		return 1
	}
	return 0
}
