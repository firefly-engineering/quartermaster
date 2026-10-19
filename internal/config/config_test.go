package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const home = "/home/u"

// write puts body in a fresh config.toml and returns its path; an empty body writes nothing.
func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func wantDefaults() Config {
	return Config{
		Vault:    Vault{Dir: "worklog", Name: "worklog"},
		Dispatch: Dispatch{WorkspaceRoot: "/home/u/.local/share/quartermaster/worklog/workspaces"},
		Harness: map[string]Harness{
			"claude": {Kind: "claude"},
		},
		Tiers:    map[string]string{"default": "claude"},
		Projects: Projects{Root: "/home/u/src/github.com", Repos: map[string]Project{}},
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	full := `
[vault]
repo = "~/src/github.com/sigma/worklog"
dir = "notes"
name = "wl"

[dispatch]
workspace_root = "/ws"

[harness.opus]
kind = "claude"
args = ["--model", "opus"]

[harness.kimi]
kind = "omp"
args = ["--model", "kimi"]

[harness.spare]
kind = "codex"

[tiers]
default = "opus"
research = "kimi"

[projects]
root = "~/code"

[projects."arbora-partners/trunk"]
path = "~/src/github.com/arbora-partners/trunk"
workspace = "trunk"

[projects."arbora-partners/IaC"]
path = "/abs/IaC"
workspace = "IaC"
`
	tests := []struct {
		name        string
		defaultFile string // body of the file at the default path
		configFile  string // body of the file named by --config
		vault       string // --vault
		want        func(*Config)
	}{
		{
			name: "defaults alone when no user file exists",
			want: func(*Config) {},
		},
		{
			name:        "the user file overrides defaults and expands ~",
			defaultFile: full,
			want: func(c *Config) {
				c.Vault = Vault{Repo: "/home/u/src/github.com/sigma/worklog", Dir: "notes", Name: "wl"}
				c.Dispatch.WorkspaceRoot = "/ws"
				c.Harness = map[string]Harness{
					"opus":  {Kind: "claude", Args: []string{"--model", "opus"}},
					"kimi":  {Kind: "omp", Args: []string{"--model", "kimi"}},
					"spare": {Kind: "codex"},
				}
				c.Tiers = map[string]string{"default": "opus", "research": "kimi"}
				c.Projects = Projects{Root: "/home/u/code", Repos: map[string]Project{
					"arbora-partners/trunk": {Path: "/home/u/src/github.com/arbora-partners/trunk", Workspace: "trunk"},
					"arbora-partners/IaC":   {Path: "/abs/IaC", Workspace: "IaC"},
				}}
			},
		},
		{
			name:        "projects.root as a dotted key, beside an override of the workspace alone",
			defaultFile: "projects.root = \"/src\"\n[projects.\"o/r\"]\nworkspace = \"w\"\n",
			want: func(c *Config) {
				c.Projects = Projects{Root: "/src", Repos: map[string]Project{"o/r": {Workspace: "w"}}}
			},
		},
		{
			name: "user harness and tiers tables replace the built-in ones whole",
			defaultFile: "[harness.claude]\nkind = \"claude\"\nargs = [\"--fast\"]\n[harness.glm_5-x]\nkind = \"omp\"\n" +
				"[tiers]\ndefault = \"claude\"\nbasic-implementation = \"glm_5-x\"\n",
			want: func(c *Config) {
				c.Harness = map[string]Harness{"claude": {Kind: "claude", Args: []string{"--fast"}}, "glm_5-x": {Kind: "omp"}}
				c.Tiers = map[string]string{"default": "claude", "basic-implementation": "glm_5-x"}
			},
		},
		{
			name:        "a harness without args keeps none, not the built-in ones",
			defaultFile: "[harness.claude]\nkind = \"claude\"\n[harness.glm]\nkind = \"omp\"\n",
			want: func(c *Config) {
				c.Harness = map[string]Harness{"claude": {Kind: "claude"}, "glm": {Kind: "omp"}}
			},
		},
		{
			name:        "user tables replace the built-in harness set and default tier whole",
			defaultFile: "[harness.glm]\nkind = \"omp\"\n[tiers]\ndefault = \"glm\"\n",
			want: func(c *Config) {
				c.Harness = map[string]Harness{"glm": {Kind: "omp"}}
				c.Tiers = map[string]string{"default": "glm"}
			},
		},
		{
			name:        "keys the user file omits keep their defaults",
			defaultFile: "[vault]\nrepo = \"/v\"\n",
			want:        func(c *Config) { c.Vault.Repo = "/v" },
		},
		{
			name:        "--config replaces the default path",
			defaultFile: "[vault]\nrepo = \"/from-default\"\n",
			configFile:  "[vault]\nname = \"flagged\"\n",
			want:        func(c *Config) { c.Vault.Name = "flagged" },
		},
		{
			name:        "--vault overrides the user file",
			defaultFile: "[vault]\nrepo = \"/from-file\"\n",
			vault:       "~/v",
			want:        func(c *Config) { c.Vault.Repo = "/home/u/v" },
		},
		{
			name:       "--vault overrides the --config file",
			configFile: "[vault]\nrepo = \"/from-file\"\n",
			vault:      "/flag",
			want:       func(c *Config) { c.Vault.Repo = "/flag" },
		},
		{
			name:        "a bare ~ is the home directory",
			defaultFile: "[dispatch]\nworkspace_root = \"~\"\n",
			want:        func(c *Config) { c.Dispatch.WorkspaceRoot = home },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := Options{Home: home, DefaultPath: write(t, tt.defaultFile), Vault: tt.vault}
			if tt.configFile != "" {
				opts.Config = write(t, tt.configFile)
			}
			got, err := Load(opts)
			if err != nil {
				t.Fatal(err)
			}
			want := wantDefaults()
			tt.want(&want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got  %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestLoadNoDefaultPath(t *testing.T) {
	t.Parallel()
	got, err := Load(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if want := wantDefaults(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.toml")
	tests := []struct {
		name   string
		file   string // body of the user file
		noHome bool
		opts   Options
		want   []string // substrings the error must contain
	}{
		{
			name: "unknown top-level key",
			file: "colour = \"red\"\n",
			want: []string{"unknown key", "colour"},
		},
		{
			name: "unknown nested key",
			file: "[vault]\nrepos = \"/v\"\n",
			want: []string{"unknown key", "vault.repos"},
		},
		{
			name: "unknown key in a project",
			file: "[projects.\"o/r\"]\npath = \"/p\"\nlabel = \"x\"\n",
			want: []string{"unknown key", "label"},
		},
		{
			name: "project key without a slash",
			file: "[projects.trunk]\npath = \"/p\"\n",
			want: []string{"projects", `"trunk"`, "owner/repo"},
		},
		{
			name: "project key with an empty owner",
			file: "[projects.\"/trunk\"]\npath = \"/p\"\n",
			want: []string{`"/trunk"`, "owner/repo"},
		},
		{
			name: "project key with an empty repo",
			file: "[projects.\"o/\"]\npath = \"/p\"\n",
			want: []string{`"o/"`, "owner/repo"},
		},
		{
			name: "project key with two slashes",
			file: "[projects.\"o/r/x\"]\npath = \"/p\"\n",
			want: []string{`"o/r/x"`, "owner/repo"},
		},
		{
			name: "project key with a forbidden character",
			file: "[projects.\"o/r#1\"]\npath = \"/p\"\n",
			want: []string{`"o/r#1"`, "owner/repo"},
		},
		{
			name: "project repo of dots",
			file: "[projects.\"o/..\"]\npath = \"/p\"\n",
			want: []string{`"o/.."`, "owner/repo"},
		},
		{
			name: "malformed TOML",
			file: "[vault\n",
			want: []string{"config.toml"},
		},
		{
			name: "wrong value type",
			file: "[vault]\nrepo = 3\n",
			want: []string{"config.toml", "repo"},
		},
		{
			name: "--config names a missing file",
			opts: Options{Config: missing},
			want: []string{"absent.toml"},
		},
		{
			name:   "~ with no home directory",
			file:   "[vault]\nrepo = \"~/v\"\n[dispatch]\nworkspace_root = \"/ws\"\n",
			noHome: true,
			want:   []string{"vault.repo", "home"},
		},
		{
			name:   "default workspace_root with no home directory",
			noHome: true,
			want:   []string{"dispatch.workspace_root", "home"},
		},
		{
			name: "relative --config",
			opts: Options{Config: "config.toml"},
			want: []string{`"config.toml"`, "absolute"},
		},
		{
			name: "~user is not expanded, so is relative",
			file: "[vault]\nrepo = \"~bob/v\"\n",
			want: []string{`"~bob/v"`, "absolute"},
		},
		{
			name: "relative --vault",
			opts: Options{Vault: "."},
			want: []string{"--vault", "absolute"},
		},
		{
			name: "relative vault.repo",
			file: "[vault]\nrepo = \"v\"\n",
			want: []string{"vault.repo", "absolute"},
		},
		{
			name: "relative project path",
			file: "[projects.\"o/r\"]\npath = \"r\"\n",
			want: []string{`projects."o/r".path`, "absolute"},
		},
		{
			name:   "default projects.root with no home directory",
			file:   "[dispatch]\nworkspace_root = \"/ws\"\n",
			noHome: true,
			want:   []string{"projects.root", "home"},
		},
		{
			name: "relative projects.root",
			file: "[projects]\nroot = \"src\"\n",
			want: []string{"projects.root", "absolute"},
		},
		{
			name: "projects.root not a string",
			file: "[projects.root]\npath = \"/p\"\n",
			want: []string{"config.toml", "projects.root"},
		},
		{
			name: "a project not a table",
			file: "[projects]\n\"o/r\" = \"/p\"\n",
			want: []string{"config.toml", `projects."o/r"`},
		},
		{
			name: "unknown key in projects",
			file: "[projects]\nroots = \"/p\"\n",
			want: []string{`"roots"`, "root or owner/repo"},
		},
		{
			name: "harness.worker is gone",
			file: "[harness.worker]\nkind = \"omp\"\n",
			want: []string{"[harness.worker] is gone: name your harnesses and map tiers to them in [tiers]"},
		},
		{
			name: "tiers without default",
			file: "[tiers]\ndesign = \"claude\"\n",
			want: []string{"[tiers]", "default"},
		},
		{
			name: "a tier not in the skill's list",
			file: "[tiers]\ndefault = \"claude\"\nlane = \"glm\"\n",
			want: []string{`"lane"`, "default", "research", "basic-implementation", "security-review"},
		},
		{
			name: "a tier naming an undefined harness",
			file: "[tiers]\ndefault = \"claude\"\ndesign = \"gpt\"\n",
			want: []string{"tiers.design", `"gpt"`, "not defined"},
		},
		{
			name: "built-in tiers naming a harness the user's set dropped",
			file: "[harness.glm]\nkind = \"omp\"\n",
			want: []string{"tiers.default", `"claude"`, "not defined"},
		},
		{
			name: "two harnesses of one kind",
			file: "[harness.claude]\nkind = \"claude\"\n[harness.glm]\nkind = \"omp\"\n[harness.kimi]\nkind = \"omp\"\n",
			want: []string{`"glm"`, `"kimi"`, `"omp"`},
		},
		{
			name: "a harness without a kind",
			file: "[harness.claude]\nkind = \"claude\"\n[harness.glm]\nargs = [\"x\"]\n",
			want: []string{"harness.glm.kind"},
		},
		{
			name: "a harness name starting with a digit",
			file: "[harness.1claude]\nkind = \"claude\"\n[tiers]\ndefault = \"1claude\"\n",
			want: []string{`"1claude"`, "[a-z][a-z0-9_-]*"},
		},
		{
			name: "a harness name with an upper-case letter",
			file: "[harness.Claude]\nkind = \"claude\"\n[tiers]\ndefault = \"Claude\"\n",
			want: []string{`"Claude"`, "[a-z][a-z0-9_-]*"},
		},
		{
			name: "a harness name with a dot",
			file: "[harness.\"c.l\"]\nkind = \"claude\"\n[tiers]\ndefault = \"c.l\"\n",
			want: []string{`"c.l"`, "[a-z][a-z0-9_-]*"},
		},
		{
			name: "unknown key in a harness",
			file: "[harness.claude]\nkind = \"claude\"\nmodel = \"opus\"\n",
			want: []string{"unknown key", "harness.claude.model"},
		},
		{
			name:   "--vault ~ with no home directory",
			file:   "[dispatch]\nworkspace_root = \"/ws\"\n",
			noHome: true,
			opts:   Options{Vault: "~/v"},
			want:   []string{"--vault", "home"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := tt.opts
			if !tt.noHome {
				opts.Home = home
			}
			if opts.Config == "" {
				opts.DefaultPath = write(t, tt.file)
			}
			_, err := Load(opts)
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestHarnessFor(t *testing.T) {
	t.Parallel()
	c := Config{
		Harness: map[string]Harness{"claude": {Kind: "claude"}, "glm": {Kind: "omp", Args: []string{"--model", "glm-5"}}},
		Tiers:   map[string]string{"default": "claude", "refactoring": "glm"},
	}
	for _, tc := range []struct {
		tier, want string
	}{
		{"refactoring", "glm"},
		{"design", "claude"},
		{"", "claude"},
	} {
		if name, h := c.HarnessFor(tc.tier); name != tc.want || h.Kind != c.Harness[tc.want].Kind {
			t.Errorf("%q: %q %+v, want %q", tc.tier, name, h, tc.want)
		}
	}
	if name, h := (Config{}).HarnessFor("design"); name != "" || h.Kind != "" {
		t.Errorf("no tiers: %q %+v", name, h)
	}
}

func TestProject(t *testing.T) {
	t.Parallel()
	p := Projects{Root: "/src", Repos: map[string]Project{
		"o/moved": {Path: "/elsewhere/moved"},
		"o/named": {Workspace: "nm"},
	}}
	for _, c := range []struct {
		repo string
		want Project
	}{
		{"o/r", Project{Path: "/src/o/r"}},
		{"other/thing", Project{Path: "/src/other/thing"}},
		{"o/moved", Project{Path: "/elsewhere/moved"}},
		{"o/named", Project{Path: "/src/o/named", Workspace: "nm"}},
	} {
		if got := p.Project(c.repo); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.repo, got, c.want)
		}
	}
	if got := (Projects{}).Project("o/r"); got != (Project{}) {
		t.Errorf("no root: %+v, want no path", got)
	}
}

func TestHarnessPrompt(t *testing.T) {
	t.Parallel()
	omp, claude := Harness{Kind: "omp"}, Harness{Kind: "claude"}
	for _, tc := range []struct {
		h          Harness
		text, want string
	}{
		{omp, "/mass-implement acme/web #20\n\nKeep it small.", "/skill:mass-implement acme/web #20\n\nKeep it small."},
		{omp, "/implement", "/skill:implement"},
		{omp, "/skill:implement acme/web#20", "/skill:implement acme/web#20"},
		{omp, "/Users/me/notes.md says why", "/Users/me/notes.md says why"},
		{omp, "Fix the form, then /implement it", "Fix the form, then /implement it"},
		{omp, "/ alone", "/ alone"},
		{claude, "/mass-implement acme/web #20", "/mass-implement acme/web #20"},
	} {
		if got := tc.h.Prompt(tc.text); got != tc.want {
			t.Errorf("%s %q: %q, want %q", tc.h.Kind, tc.text, got, tc.want)
		}
	}
}
