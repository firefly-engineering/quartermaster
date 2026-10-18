package main

import (
	"bytes"
	"io/fs"
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
