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
		Projects: map[string]Project{},
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

[harness.worker]
kind = "omp"
args = ["--model", "glm-5"]

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
				c.Harness.Worker = Worker{Kind: "omp", Args: []string{"--model", "glm-5"}}
				c.Projects = map[string]Project{
					"arbora-partners/trunk": {Path: "/home/u/src/github.com/arbora-partners/trunk", Workspace: "trunk"},
					"arbora-partners/IaC":   {Path: "/abs/IaC", Workspace: "IaC"},
				}
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
