package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/skills"
)

// TestDeps hands the tools the paths of jj, git, gh and herdr found on PATH, and the environment.
func TestDeps(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	h.environ = []string{"A=1"}
	h.lookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	d := deps(config.Config{}, h)
	if d.JJ != "/bin/jj" || d.Git != "/bin/git" || d.GH != "/bin/gh" || d.Herdr != "/bin/herdr" || len(d.Env) != 1 {
		t.Errorf("deps %+v", d)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb, testHost(t)); code != 0 || out.String() != version+"\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb, testHost(t)); code != 2 || errb.Len() == 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestSendUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"send"}, {"send", "/vault", "2026-10-16"}, {"send", "/v", "2026-10-16", "a", "b"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb, testHost(t)); code != 2 || !strings.Contains(errb.String(), "send <vault> <date> <item-id>") {
			t.Fatalf("%q: code %d, stderr %q", args, code, errb.String())
		}
	}
}

func TestSendNoHerdr(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	repo := t.TempDir()
	vault := filepath.Join(repo, "worklog")
	cfg := filepath.Join(h.home, ".config", "quartermaster", "worklog.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[vault]\nrepo = "+strconv.Quote(repo)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	// The inputs are validated before herdr is needed.
	code := send([]string{vault, "2026-10-16", "x;rm"}, &out, &errb, h)
	if code != 1 || errb.String() != "worklog send: not an item id: \"x;rm\"\n" {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	note := "# Worklog — 2026-10-16\n\n## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n" +
		"##### ⬜ Go\n\nGo. ^a\n\n> [!prompt]- Prompt\n> ```herdr-prompt id=a to=t\n> go\n> ```\n"
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "2026-10-16.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	code = send([]string{vault, "2026-10-16", "a"}, &out, &errb, h)
	if code != 1 || errb.String() != "worklog send: herdr not found on PATH\n" {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	// The vault the button names must be the configured one, where the send records its pane.
	errb.Reset()
	code = send([]string{t.TempDir(), "2026-10-16", "a"}, &out, &errb, h)
	if code != 1 || !strings.Contains(errb.String(), "is not the configured one") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, xdg, home, want string
	}{
		{"XDG_CONFIG_HOME wins", "/xdg", "/home/u", "/xdg/quartermaster/worklog.toml"},
		{"unset falls back to ~/.config", "", "/home/u", "/home/u/.config/quartermaster/worklog.toml"},
		{"a relative XDG_CONFIG_HOME is ignored", "rel", "/home/u", "/home/u/.config/quartermaster/worklog.toml"},
		{"no home and no XDG_CONFIG_HOME gives no path", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(k string) string {
				if k == "XDG_CONFIG_HOME" {
					return tt.xdg
				}
				return ""
			}
			if got := defaultConfigPath(getenv, tt.home); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSkillEmbedded(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"worklog/SKILL.md", "worklog/template.md"} {
		if b, err := fs.ReadFile(skills.Worklog, name); err != nil || len(b) == 0 {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(b))
		}
	}
}

func TestServeTakesNoArguments(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "extra"}, &out, &errb, testHost(t)); code != 2 || errb.Len() == 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

// testHost stands in for the process: no environment, a temp home and cwd, a stopped clock and
// no tools on PATH.
func testHost(t *testing.T) host {
	t.Helper()
	return host{
		getenv:   func(string) string { return "" },
		home:     t.TempDir(),
		cwd:      t.TempDir(),
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		now:      func() time.Time { return time.Date(2026, 10, 19, 8, 30, 0, 0, time.UTC) },
	}
}

// TestNoteOpenRead opens and reads a note through `note`, with the vault from a relative
// --vault resolved against the cwd.
func TestNoteOpenRead(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	if err := os.MkdirAll(filepath.Join(h.cwd, "repo", "worklog"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"note", "--vault", "repo", "open"}, &out, &errb, h); code != 0 {
		t.Fatalf("open: code %d, stderr %q", code, errb.String())
	}
	var opened struct{ Path, Date string }
	if err := json.Unmarshal(out.Bytes(), &opened); err != nil ||
		opened.Path != filepath.Join(h.cwd, "repo", "worklog", "2026-10-19.md") || opened.Date != "2026-10-19" {
		t.Fatalf("open: %v %s", err, out.String())
	}
	out.Reset()
	if code := run([]string{"note", "--vault", "repo", "read", `{"date":"2026-10-19"}`}, &out, &errb, h); code != 0 {
		t.Fatalf("read: code %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), `"date": "2026-10-19"`) {
		t.Fatalf("read: %s", out.String())
	}
	errb.Reset()
	if code := run([]string{"note", "--vault", "repo", "read", `{"date":"2026-10-18"}`}, &out, &errb, h); code != 1 ||
		!strings.Contains(errb.String(), "worklog note read: ") {
		t.Fatalf("read of a missing note: code %d, stderr %q", code, errb.String())
	}
}

// TestNoteConfig reads the vault from the default config file, under the host's home.
func TestNoteConfig(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(h.home, ".config", "quartermaster", "worklog.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[vault]\nrepo = \""+repo+"\"\ndir = \"notes\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"note", "open"}, &out, &errb, h); code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "notes", "2026-10-19.md")); err != nil {
		t.Fatal(err)
	}
}

func TestNoteUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"note"}, {"note", "close"}, {"note", "--bogus", "read"}, {"note", "read", "{}", "x"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb, testHost(t)); code != 2 || !strings.Contains(errb.String(), "note [--config <file>]") {
			t.Fatalf("%q: code %d, stderr %q", args, code, errb.String())
		}
	}
}

// TestBadConfig fails on a --config that does not exist, resolved against the cwd.
func TestBadConfig(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	for _, args := range [][]string{{"serve", "--config", "missing.toml"}, {"note", "--config", "missing.toml", "read"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb, h); code != 1 || !strings.Contains(errb.String(), filepath.Join(h.cwd, "missing.toml")) {
			t.Fatalf("%q: code %d, stderr %q", args, code, errb.String())
		}
	}
}

func TestNoteNoVault(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"note", "read"}, &out, &errb, testHost(t)); code != 1 || !strings.Contains(errb.String(), "vault.repo is not set") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}
