// Package config loads the worklog's configuration (docs/spec.md §3): built-in defaults,
// then the user's worklog.toml, then the --config / --vault flags, later sources winning.
//
// The package reads neither the environment nor the cwd: main resolves the default path and
// the home directory and passes them in Options.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/skills"
)

// Config is the resolved configuration. Every path in it has had `~` expanded.
type Config struct {
	Vault    Vault              `toml:"vault"`
	Dispatch Dispatch           `toml:"dispatch"`
	Harness  map[string]Harness `toml:"harness"` // keyed by the harness's name
	Tiers    map[string]string  `toml:"tiers"`   // tier, or default, to a harness's name
	Projects Projects           `toml:"-"`       // decodeFile reads the projects table key by key: see Projects
}

// Vault locates the worklog vault.
type Vault struct {
	Repo string `toml:"repo"` // jj repo holding the vault
	Dir  string `toml:"dir"`  // Obsidian vault, relative to Repo
	Name string `toml:"name"` // Obsidian vault name, for obsidian:// URIs
}

// Dispatch configures task dispatch.
type Dispatch struct {
	WorkspaceRoot string `toml:"workspace_root"` // <root>/<owner>/<repo>/issue-<n>, or <item-id> for a batch
}

// Harness is an agent program and how herdr starts it: its herdr agent kind and its arguments
// (ADR 0011). Config holds at most one harness per kind.
type Harness struct {
	Kind string   `toml:"kind"`
	Args []string `toml:"args"`
}

// Prompt is text as h's harness runs it. The note writes a route's command as `/<name>`,
// which claude runs as a skill; omp runs a skill only as `/skill:<name>`, so for kind omp a
// leading `/<name>` becomes that. Text led by a path or an already-qualified command is kept.
func (h Harness) Prompt(text string) string {
	rest, ok := strings.CutPrefix(text, "/")
	if h.Kind != "omp" || !ok {
		return text
	}
	name := rest
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		name = rest[:end]
	}
	if name == "" || strings.ContainsAny(name, "/:") {
		return text
	}
	return "/skill:" + rest
}

// DefaultTier is the [tiers] key every tier it does not list, and every item with no tier,
// goes to.
const DefaultTier = "default"

// HarnessFor resolves tier, "" for an item with none, to its harness through [tiers], falling
// back to [tiers] default. name is "" when Tiers names no harness for it, as in a Config not
// built by Load.
func (c Config) HarnessFor(tier string) (name string, h Harness) {
	name, ok := c.Tiers[tier]
	if !ok {
		name = c.Tiers[DefaultTier]
	}
	h, ok = c.Harness[name]
	if !ok {
		return "", Harness{}
	}
	return name, h
}

// Projects locates every GitHub repo's checkout by convention, at <Root>/<owner>/<repo>; an
// entry in Repos overrides it. In TOML both live in the projects table: `root` is a string,
// every other key an owner/repo table, and no owner/repo is named root.
type Projects struct {
	Root  string             // projects.root
	Repos map[string]Project // projects."owner/repo", keyed by owner/repo (ADR 0007)
}

// Project is one GitHub repo's checkout and herdr session. In an override, an empty field
// keeps the convention.
type Project struct {
	Path      string `toml:"path"`      // checkout
	Workspace string `toml:"workspace"` // herdr workspace label of the project's session
}

// Project resolves repo, an owner/repo: its checkout is its entry's path, else
// <Root>/<owner>/<repo> ("" when Root is unset); its herdr workspace label is its entry's, or
// "" for the caller to find in herdr's live state (spec §9.2).
func (p Projects) Project(repo string) Project {
	pr := p.Repos[repo]
	if owner, name, ok := strings.Cut(repo, "/"); ok && pr.Path == "" && p.Root != "" {
		pr.Path = filepath.Join(p.Root, owner, name)
	}
	return pr
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
		Dispatch: Dispatch{WorkspaceRoot: "~/.local/share/quartermaster/worklog/workspaces"},
		Harness: map[string]Harness{
			"claude": {Kind: "claude"},
			"glm":    {Kind: "omp", Args: []string{"--model", "glm-5"}},
		},
		Tiers:    map[string]string{DefaultTier: "claude", "basic-implementation": "glm", "refactoring": "glm"},
		Projects: Projects{Root: "~/src/github.com", Repos: map[string]Project{}},
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

	if err := validateHarnesses(cfg); err != nil {
		return Config{}, err
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
	// The projects table holds the root string beside owner/repo tables, so it is decoded key
	// by key. vault and dispatch decode into cfg in place, keeping the defaults the file omits;
	// harness and tiers, when the file has them, replace the built-in tables whole.
	file := struct {
		Vault    *Vault                    `toml:"vault"`
		Dispatch *Dispatch                 `toml:"dispatch"`
		Harness  map[string]Harness        `toml:"harness"`
		Tiers    map[string]string         `toml:"tiers"`
		Projects map[string]toml.Primitive `toml:"projects"`
	}{Vault: &cfg.Vault, Dispatch: &cfg.Dispatch}
	md, err := toml.Decode(string(b), &file)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if md.IsDefined("harness") {
		cfg.Harness = file.Harness
	}
	if md.IsDefined("tiers") {
		cfg.Tiers = file.Tiers
	}
	for key, v := range file.Projects {
		if key == "root" {
			if err := md.PrimitiveDecode(v, &cfg.Projects.Root); err != nil {
				return fmt.Errorf("%s: projects.root: %w", path, err)
			}
			continue
		}
		if !validRef(key) {
			return fmt.Errorf("%s: projects.%s: want root or owner/repo", path, strconv.Quote(key))
		}
		var p Project
		if err := md.PrimitiveDecode(v, &p); err != nil {
			return fmt.Errorf("%s: projects.%s: %w", path, strconv.Quote(key), err)
		}
		cfg.Projects.Repos[key] = p
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = k.String()
		}
		return fmt.Errorf("%s: unknown key %s", path, strings.Join(names, ", "))
	}
	return nil
}

// reservedHarness is the name of the one harness config had before tiers.
const reservedHarness = "worker"

// validateHarnesses checks the merged harness and tiers tables (ADR 0011): every harness well
// named and of its own kind, and every tier, default included, routed to one of them. A
// harness no tier names is allowed.
func validateHarnesses(cfg Config) error {
	if _, ok := cfg.Harness[reservedHarness]; ok {
		return errors.New("[harness.worker] is gone: name your harnesses and map tiers to them in [tiers]")
	}
	byKind := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(cfg.Harness)) {
		if !validHarnessName(name) {
			return fmt.Errorf("harness.%s: a harness name must match [a-z][a-z0-9_-]*", strconv.Quote(name))
		}
		kind := cfg.Harness[name].Kind
		if kind == "" {
			return fmt.Errorf("harness.%s.kind is not set: name the herdr agent kind it starts", name)
		}
		if other, ok := byKind[kind]; ok {
			return fmt.Errorf("harnesses %q and %q are both of kind %q: keep one harness per kind", other, name, kind)
		}
		byKind[kind] = name
	}
	if _, ok := cfg.Tiers[DefaultTier]; !ok {
		return errors.New("[tiers] has no default: every tier it does not list goes there")
	}
	tiers, err := skills.Tiers()
	if err != nil {
		return err
	}
	for _, tier := range slices.Sorted(maps.Keys(cfg.Tiers)) {
		if tier != DefaultTier && !slices.Contains(tiers, tier) {
			return fmt.Errorf("tiers.%s: not a tier; want one of %s", strconv.Quote(tier),
				strings.Join(append([]string{DefaultTier}, tiers...), ", "))
		}
		if _, ok := cfg.Harness[cfg.Tiers[tier]]; !ok {
			return fmt.Errorf("tiers.%s: harness %q is not defined", tier, cfg.Tiers[tier])
		}
	}
	return nil
}

// validHarnessName reports whether name matches [a-z][a-z0-9_-]*, so it fits a herdr agent name.
func validHarnessName(name string) bool {
	return name != "" && herdr.NameStart(rune(name[0])) && !strings.ContainsFunc(name, func(r rune) bool { return !herdr.NameChar(r) })
}

func expandPaths(cfg *Config, home string) error {
	fields := []struct {
		name string
		p    *string
	}{
		{"vault.repo", &cfg.Vault.Repo},
		{"dispatch.workspace_root", &cfg.Dispatch.WorkspaceRoot},
		{"projects.root", &cfg.Projects.Root},
	}
	for _, f := range fields {
		v, err := expand(*f.p, home)
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		*f.p = v
	}
	for key, p := range cfg.Projects.Repos {
		v, err := expand(p.Path, home)
		if err != nil {
			return fmt.Errorf("projects.%s.path: %w", strconv.Quote(key), err)
		}
		p.Path = v
		cfg.Projects.Repos[key] = p
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
