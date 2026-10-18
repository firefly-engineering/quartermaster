// Command quartermaster keeps the day's worklog note and dispatches its work (docs/spec.md).
//
// main is the one place that reads the process's environment, home, cwd and clock and finds
// jj and herdr on PATH; it passes what it found down to every package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/refresh"
	qmsend "github.com/firefly-engineering/quartermaster/internal/send"
	"github.com/firefly-engineering/quartermaster/internal/server"
	"github.com/firefly-engineering/quartermaster/internal/tools"
)

var version = "dev"

// host is what main reads from the process, gathered once so tests can stand in for it.
type host struct {
	getenv   func(string) string
	environ  []string // handed to jj and herdr
	home     string   // "" when unknown
	cwd      string   // relative --config and --vault resolve against it; "" when unknown
	lookPath func(string) (string, error)
	now      func() time.Time
}

func osHost() host {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	return host{
		getenv: os.Getenv, environ: os.Environ(), home: home, cwd: cwd,
		lookPath: exec.LookPath, now: time.Now,
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, osHost()))
}

func usage(stderr io.Writer) int {
	verbs := strings.Join(tools.Verbs(tools.All(tools.Deps{})), "|")
	_, _ = fmt.Fprintln(stderr, "usage: quartermaster version\n"+
		"       quartermaster serve [--config <file>] [--vault <repo>]\n"+
		"       quartermaster note [--config <file>] [--vault <repo>] "+verbs+" [<json input>]\n"+
		"       quartermaster send <vault> <date> <item-id>")
	return 2
}

func run(args []string, stdout, stderr io.Writer, h host) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return usage(stderr)
		}
		if _, err := fmt.Fprintln(stdout, version); err != nil {
			return 1
		}
		return 0
	case "serve":
		return serve(args[1:], stderr, h)
	case "note":
		return note(args[1:], stdout, stderr, h)
	case "send":
		if len(args) != 4 {
			return usage(stderr)
		}
		return send(args[1:], stdout, stderr, h.lookPath, h.environ)
	}
	return usage(stderr)
}

// send runs the ▶ button handler (spec §9.1) with herdr found by lookPath and given env. A
// herdr not found stays "", which Send reports only once the date and id are valid.
func send(args []string, stdout, stderr io.Writer, lookPath func(string) (string, error), env []string) int {
	herdr, _ := lookPath("herdr")
	sent, err := qmsend.Sender{Herdr: herdr, Env: env}.Send(context.Background(), args[0], args[1], args[2])
	if err == nil {
		_, err = fmt.Fprintln(stdout, sent)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "quartermaster send:", err)
		return 1
	}
	return 0
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

// errUsage is a command line that does not parse.
var errUsage = errors.New("usage")

// configFlags parses the --config and --vault flags of a subcommand, then loads the
// configuration. It returns the arguments after the flags.
func configFlags(name string, args []string, stderr io.Writer, h host) (config.Config, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgFile := fs.String("config", "", "the config file, instead of the default one")
	vault := fs.String("vault", "", "the repo holding the vault, overriding vault.repo")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, nil, errUsage // the flag package has said why
	}
	cfg, err := config.Load(config.Options{
		Home:        h.home,
		DefaultPath: defaultConfigPath(h.getenv, h.home),
		Config:      absolute(*cfgFile, h.cwd),
		Vault:       absolute(*vault, h.cwd),
	})
	return cfg, fs.Args(), err
}

// absolute resolves a relative flag path against cwd, which config.Load never does. A path
// starting with `~` is left for Load to expand.
func absolute(p, cwd string) string {
	if p == "" || filepath.IsAbs(p) || p == "~" || strings.HasPrefix(p, "~/") || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

// deps are the tools' dependencies, from the configuration and the host.
func deps(cfg config.Config, h host) tools.Deps {
	jj, _ := h.lookPath("jj") // "" is fine until the vault turns out to be a jj repo
	gh, _ := h.lookPath("gh") // "" makes refresh report gh missing, per item
	herdr, _ := h.lookPath("herdr")
	return tools.Deps{
		Config: cfg, Now: h.now, JJ: jj, Env: h.environ,
		GH: refresh.Command("gh", gh, h.environ), Herdr: refresh.Command("herdr", herdr, h.environ),
	}
}

// serve runs the MCP server on stdin and stdout until the client disconnects.
func serve(args []string, stderr io.Writer, h host) int {
	cfg, rest, err := configFlags("serve", args, stderr, h)
	if errors.Is(err, errUsage) || err == nil && len(rest) > 0 {
		return usage(stderr)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "quartermaster serve:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := server.New(version, tools.All(deps(cfg, h))...)
	if err == nil {
		err = s.Run(ctx, &mcp.StdioTransport{})
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "quartermaster serve:", err)
		return 1
	}
	return 0
}

// note runs one tool as a verb: its input is one JSON object argument, `{}` when absent, and
// its output JSON is printed.
func note(args []string, stdout, stderr io.Writer, h host) int {
	cfg, rest, err := configFlags("note", args, stderr, h)
	if errors.Is(err, errUsage) || err == nil && (len(rest) == 0 || len(rest) > 2) {
		return usage(stderr)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "quartermaster note:", err)
		return 1
	}
	var input []byte
	if len(rest) == 2 {
		input = []byte(rest[1])
	}
	err = tools.RunVerb(context.Background(), tools.All(deps(cfg, h)), rest[0], input, stdout)
	if errors.Is(err, tools.ErrUnknownVerb) {
		return usage(stderr)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "quartermaster note %s: %v\n", rest[0], err)
		return 1
	}
	return 0
}
