package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/send"
)

// fakeHerdr writes a herdr stand-in with one workspace, web, holding one agent of status, and
// returns its path and the file it records `agent prompt <pane> <text>` in.
func fakeHerdr(t *testing.T, status string) (bin, sent string) {
	t.Helper()
	dir := t.TempDir()
	bin, sent = filepath.Join(dir, "herdr"), filepath.Join(dir, "sent")
	script := `#!/bin/sh
case "$1 $2" in
  "workspace list") echo '{"result":{"workspaces":[{"workspace_id":"w1","label":"web"}]}}' ;;
  "agent list") echo '{"result":{"agents":[{"workspace_id":"w1","pane_id":"p1","agent_status":"` + status + `"}]}}' ;;
  "agent prompt") printf '%s\n%s' "$3" "$4" > '` + sent + `' ;;
  *) echo "unexpected: $*" >&2; exit 3 ;;
esac
`
	// Hold ForkLock so no parallel fork inherits the write fd: golang/go#22315 (ETXTBSY).
	syscall.ForkLock.RLock()
	err := os.WriteFile(bin, []byte(script), 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	return bin, sent
}

// TestPromptSend sends a work item's prompt through a fake herdr, as a verb, and leaves the
// note as it was.
func TestPromptSend(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	var sent string
	d.Herdr, sent = fakeHerdr(t, "idle")
	path := filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir, fixtureDate+".md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := RunVerb(t.Context(), All(d), "send", []byte(`{"item":"session-store"}`), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"item": "session-store"`, `"to": "web"`, `"pane": "p1"`, `"path": "` + path + `"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %s:\n%s", want, out.String())
		}
	}
	got, err := os.ReadFile(sent)
	if err != nil {
		t.Fatal(err)
	}
	if want := "p1\n/grill-with-docs https://github.com/acme/web/issues/14\n\n" +
		send.Closing(send.Item("session-store"), path); string(got) != want {
		t.Errorf("herdr got\n%s\nwant\n%s", got, want)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the note changed: %v", err)
	}
}

// TestPromptSendRefuses checks prompt_send keeps send's validation and prompts no agent when
// it refuses.
func TestPromptSendRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, status string
		in           PromptSendInput
		want         string
	}{
		{"no herdr", "", PromptSendInput{Item: "session-store"}, "herdr not found"},
		{"a bad date", "idle", PromptSendInput{Date: "today", Item: "session-store"}, "not a date"},
		{"a bad id", "idle", PromptSendInput{Item: "../x"}, "not an item id"},
		{"no note", "idle", PromptSendInput{Date: "2026-10-15", Item: "session-store"}, "no note"},
		{"no prompt", "idle", PromptSendInput{Item: "nope"}, "no herdr-prompt block"},
		{"a blocked agent", "blocked", PromptSendInput{Item: "session-store"}, "web agent is blocked"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, settled(t), false)
			var sent string
			d.Herdr, sent = fakeHerdr(t, c.status)
			if c.status == "" {
				d.Herdr = ""
			}
			if _, err := d.promptSend(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%v, want %q", err, c.want)
			}
			if _, err := os.Stat(sent); !os.IsNotExist(err) {
				t.Errorf("herdr was prompted: %v", err)
			}
		})
	}
}
