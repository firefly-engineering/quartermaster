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

func TestSkillEmbedded(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"worklog/SKILL.md", "worklog/template.md"} {
		if b, err := fs.ReadFile(skills.Worklog, name); err != nil || len(b) == 0 {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(b))
		}
	}
}
