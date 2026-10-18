// Package config loads quartermaster's configuration (docs/spec.md §3): built-in defaults,
// then the user's config.toml, then the --config / --vault flags, later sources winning.
//
// The package reads neither the environment nor the cwd: main resolves the default path and
// the home directory and passes them in Options.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the resolved configuration. Every path in it has had `~` expanded.
type Config struct {
	Vault    Vault              `toml:"vault"`
	Dispatch Dispatch           `toml:"dispatch"`
	Harness  Harness            `toml:"harness"`
	Projects map[string]Project `toml:"projects"` // keyed by owner/repo (ADR 0007)
}

// Vault locates the worklog vault.
type Vault struct {
	Repo string `toml:"repo"` // jj repo holding the vault
	Dir  string `toml:"dir"`  // Obsidian vault, relative to Repo
	Name string `toml:"name"` // Obsidian vault name, for obsidian:// URIs
}

// Dispatch configures task dispatch.
type Dispatch struct {
	WorkspaceRoot string `toml:"workspace_root"` // <root>/<owner>/<repo>/issue-<n>
}

// Harness names the agent harnesses dispatch starts.
type Harness struct {
	Worker Worker `toml:"worker"` // default harness for task_dispatch mode=fresh
}

// Worker is a herdr agent kind and its arguments.
type Worker struct {
	Kind string   `toml:"kind"`
	Args []string `toml:"args"`
}

// Project is one GitHub repo's checkout and herdr session.
type Project struct {
	Path      string `toml:"path"`      // checkout
	Workspace string `toml:"workspace"` // herdr workspace label of the project's session
}

// Options are Load's inputs, all resolved by main. Every path, here and in the user file, must
// be absolute once `~` is expanded: the package never resolves against the cwd.
type Options struct {
	Home        string // home directory `~` expands to; empty makes any `~` an error
	DefaultPath string // the user file's default path; a missing file there is not an error
	Config      string // --config: replaces DefaultPath, and must exist
	Vault       string // --vault: overrides vault.repo
}

// Defaults returns the built-in configuration, before `~` expansion.
func Defaults() Config {
	return Config{
		Vault:    Vault{Dir: "worklog", Name: "worklog"},
		Dispatch: Dispatch{WorkspaceRoot: "~/.local/share/quartermaster/workspaces"},
		Projects: map[string]Project{},
	}
}

// Load layers the defaults, the user file and the flags, then expands `~` in every path.
func Load(opts Options) (Config, error) {
	cfg := Defaults()

	path, required := opts.DefaultPath, false
	if opts.Config != "" {
		path, required = opts.Config, true
	}
	if path != "" && !filepath.IsAbs(path) {
		return Config{}, fmt.Errorf("config file %q is not an absolute path", path)
	}
	if path != "" {
		if err := decodeFile(path, required, &cfg); err != nil {
			return Config{}, err
		}
	}

	if opts.Vault != "" {
		repo, err := expand(opts.Vault, opts.Home)
		if err != nil {
			return Config{}, fmt.Errorf("--vault: %w", err)
		}
		cfg.Vault.Repo = repo
	}

	if err := expandPaths(&cfg, opts.Home); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func decodeFile(path string, required bool, cfg *Config) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return err
	}
	md, err := toml.Decode(string(b), cfg)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = k.String()
		}
		return fmt.Errorf("%s: unknown key %s", path, strings.Join(names, ", "))
	}
	for key := range cfg.Projects {
		if !validRef(key) {
			return fmt.Errorf("%s: projects.%s: want owner/repo", path, strconv.Quote(key))
		}
	}
	return nil
}

func expandPaths(cfg *Config, home string) error {
	fields := []struct {
		name string
		p    *string
	}{
		{"vault.repo", &cfg.Vault.Repo},
		{"dispatch.workspace_root", &cfg.Dispatch.WorkspaceRoot},
	}
	for _, f := range fields {
		v, err := expand(*f.p, home)
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		*f.p = v
	}
	for key, p := range cfg.Projects {
		v, err := expand(p.Path, home)
		if err != nil {
			return fmt.Errorf("projects.%s.path: %w", strconv.Quote(key), err)
		}
		p.Path = v
		cfg.Projects[key] = p
	}
	return nil
}

// expand replaces a leading `~` or `~/` with home, then requires an absolute path: a relative
// one would resolve against the cwd. An empty path stays empty, for "unset".
func expand(p, home string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home == "" {
			return "", fmt.Errorf("%q needs a home directory, and none is known", p)
		}
		p = filepath.Join(home, p[1:])
	}
	if p != "" && !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	return p, nil
}

// validRef reports whether key is owner/repo: two non-empty GitHub names joined by one slash.
func validRef(key string) bool {
	owner, repo, ok := strings.Cut(key, "/")
	return ok && validOwner(owner) && validRepo(repo)
}

// validOwner accepts letters, digits and hyphens.
func validOwner(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return !nameRune(r) })
}

// validRepo also accepts underscores and dots, but not the names "." and "..".
func validRepo(s string) bool {
	return s != "" && s != "." && s != ".." &&
		!strings.ContainsFunc(s, func(r rune) bool { return !nameRune(r) && r != '_' && r != '.' })
}

func nameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
}
