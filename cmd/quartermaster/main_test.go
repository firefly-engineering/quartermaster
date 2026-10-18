package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/skills"
)

func TestVersion(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || out.String() != version+"\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 || errb.Len() == 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestSendUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"send"}, {"send", "/vault", "2026-10-16"}, {"send", "/v", "2026-10-16", "a", "b"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 || !strings.Contains(errb.String(), "send <vault> <date> <item-id>") {
			t.Fatalf("%q: code %d, stderr %q", args, code, errb.String())
		}
	}
}

func TestSendNoHerdr(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	lookPath := func(string) (string, error) { return "", errors.New("not found") }
	// The inputs are validated before herdr is needed.
	code := send([]string{t.TempDir(), "2026-10-16", "x;rm"}, &out, &errb, lookPath, nil)
	if code != 1 || errb.String() != "quartermaster send: not an item id: \"x;rm\"\n" {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	vault := t.TempDir()
	note := "# Worklog — 2026-10-16\n\n## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n" +
		"##### ⬜ Go\n\nGo. ^a\n\n> [!prompt]- Prompt\n> ```herdr-prompt id=a to=t\n> go\n> ```\n"
	if err := os.WriteFile(filepath.Join(vault, "2026-10-16.md"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	code = send([]string{vault, "2026-10-16", "a"}, &out, &errb, lookPath, nil)
	if code != 1 || errb.String() != "quartermaster send: herdr not found on PATH\n" {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, xdg, home, want string
	}{
		{"XDG_CONFIG_HOME wins", "/xdg", "/home/u", "/xdg/quartermaster/config.toml"},
		{"unset falls back to ~/.config", "", "/home/u", "/home/u/.config/quartermaster/config.toml"},
		{"a relative XDG_CONFIG_HOME is ignored", "rel", "/home/u", "/home/u/.config/quartermaster/config.toml"},
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
	if code := run([]string{"serve", "extra"}, &out, &errb); code != 2 || errb.Len() == 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}
