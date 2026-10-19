package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

// webAgent is web's one agent, of status.
func webAgent(status string) herdr.Agent { return herdrAgent("claude", "", status, "/src/web", 1) }

// TestPromptSend sends a work item's prompt through a fake herdr, as a verb, and leaves the
// note as it was.
func TestPromptSend(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	h := newFakeHerdr(t, webAgent("idle"))
	d.Herdr = h.Bin
	path := filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir, fixtureDate+".md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := RunVerb(t.Context(), All(d), "send", []byte(`{"item":"session-store"}`), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"item": "session-store"`, `"to": "web"`, `"pane": "w1:p1"`, `"path": "` + path + `"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %s:\n%s", want, out.String())
		}
	}
	if want := "[workspace][list]\n[agent][list]\n[agent][prompt][w1:p1][/grill-with-docs https://github.com/acme/web/issues/14\n\n" +
		send.Closing(send.Item("session-store"), path) + "]\n"; h.calls(t) != want {
		t.Errorf("herdr got\n%s\nwant\n%s", h.calls(t), want)
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
			h := newFakeHerdr(t, webAgent(c.status))
			if d.Herdr = h.Bin; c.status == "" {
				d.Herdr = ""
			}
			if _, err := d.promptSend(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%v, want %q", err, c.want)
			}
			if calls := h.calls(t); strings.Contains(calls, "[agent][prompt]") {
				t.Errorf("herdr was prompted:\n%s", calls)
			}
		})
	}
}
