package send

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

const fence = "```"

// testNote holds the self-check's two prompts in the format the parser places (spec §4.1):
// a one-line prompt, and one whose body keeps a relative indent through the callout.
const testNote = `# Worklog — 2026-10-16

## ⬜ o/r

### ⬜ What remains

#### ⬜ Agent queue

##### ⬜ Run batch

[▶ Run batch](obsidian://shell-commands/?execute=x&_date=2026-10-16&_item=batch-c) ^batch-c

> [!prompt]- Prompt sent to trunk
> ` + fence + `herdr-prompt id=batch-c to=trunk
> /mass-implement arbora-partners/trunk #871 #819
> ` + fence + `

#### ⬜ Sent to other repos

##### ⬜ Deleter SA for IaC

To send. ^ask-iac

> [!prompt]- Prompt sent to IaC
> ` + fence + `herdr-prompt id=ask-iac to=IaC
> Please create the deleter SA.
>   Indented line.
> Reply with links.
> ` + fence + `

##### ⬜ No prompt

Nothing to send. ^bare
`

// writeHerdr writes a herdr stand-in at path.
func writeHerdr(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// listing is a herdr stand-in answering `workspace list` and `agent list` with spaces and
// agents, and recording `agent prompt <pane> <text>` in sentFile.
func listing(spaces, agents, sentFile string) string {
	return `case "$1 $2" in
  "workspace list") echo '` + spaces + `' ;;
  "agent list") echo '` + agents + `' ;;
  "agent prompt") printf '%s\n%s' "$3" "$4" > '` + sentFile + `' ;;
  *) echo "unexpected: $*" >&2; exit 3 ;;
esac
`
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// setup returns a vault holding the note and a Sender wired to a fake herdr: workspaces
// trunk (wZ) and IaC (wX), one agent in each, trunk's with status.
func setup(t *testing.T, status string) (vault string, s Sender) {
	t.Helper()
	vault = t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "2026-10-16.md"), []byte(testNote), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	s = Sender{Herdr: filepath.Join(bin, "herdr")}
	spaces := mustJSON(t, map[string]any{"result": map[string]any{"workspaces": []map[string]string{
		{"workspace_id": "wZ", "label": "trunk"}, {"workspace_id": "wX", "label": "IaC"},
	}}})
	agents := mustJSON(t, map[string]any{"result": map[string]any{"agents": []map[string]string{
		{"workspace_id": "wZ", "pane_id": "wZ:p6", "agent_status": status},
		{"workspace_id": "wX", "pane_id": "wX:p9", "agent_status": "idle"},
	}}})
	writeHerdr(t, s.Herdr, listing(spaces, agents, sentFile(s)))
	return vault, s
}

func sentFile(s Sender) string { return filepath.Join(filepath.Dir(s.Herdr), "sent") }

func sent(t *testing.T, s Sender) string {
	t.Helper()
	b, err := os.ReadFile(sentFile(s))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertNothingSent(t *testing.T, s Sender) {
	t.Helper()
	if _, err := os.Stat(sentFile(s)); err == nil {
		t.Fatal("a prompt was sent")
	}
}

func TestNoteParses(t *testing.T) {
	t.Parallel()
	if _, err := note.Parse([]byte(testNote)); err != nil {
		t.Fatal(err)
	}
}

func TestSendOneLinePrompt(t *testing.T) {
	t.Parallel()
	vault, s := setup(t, "idle")
	got, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Sent{ID: "batch-c", To: "trunk", Pane: "wZ:p6"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.String() != "sent batch-c to trunk (wZ:p6)" {
		t.Fatalf("message %q", got.String())
	}
	out := sent(t, s)
	if !strings.HasPrefix(out, "wZ:p6\n/mass-implement arbora-partners/trunk #871 #819\n\nWhen this is done, update the worklog") {
		t.Fatalf("sent %q", out)
	}
	want := "^batch-c in " + filepath.Join(vault, "2026-10-16.md") + ". Record the outcome and refresh Next."
	if !strings.HasSuffix(out, want) {
		t.Fatalf("sent %q, want suffix %q", out, want)
	}
}

func TestSendKeepsRelativeIndent(t *testing.T) {
	t.Parallel()
	vault, s := setup(t, "idle")
	if _, err := s.Send(context.Background(), vault, "2026-10-16", "ask-iac"); err != nil {
		t.Fatal(err)
	}
	if out := sent(t, s); !strings.HasPrefix(out, "wX:p9\nPlease create the deleter SA.\n  Indented line.\nReply with links.\n\n") {
		t.Fatalf("sent %q", out)
	}
}

func TestSendSendableStatuses(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"idle", "done", "working"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			vault, s := setup(t, status)
			if _, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSendRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, date, id, status, want string
	}{
		{"an id with shell syntax", "2026-10-16", "x;rm", "idle", "not an item id"},
		{"an uppercase id", "2026-10-16", "Batch-c", "idle", "not an item id"},
		{"an empty id", "2026-10-16", "", "idle", "not an item id"},
		{"a path for a date", "../etc", "batch-c", "idle", "not a date"},
		{"an impossible date", "2026-02-30", "batch-c", "idle", "not a date"},
		{"a date with no note", "2026-10-17", "batch-c", "idle", "no note"},
		{"an id with no item", "2026-10-16", "missing", "idle", "no herdr-prompt block"},
		{"an item with no prompt", "2026-10-16", "bare", "idle", "no herdr-prompt block"},
		{"a blocked agent", "2026-10-16", "batch-c", "blocked", "trunk agent is blocked; answer it first"},
		{"an unknown status", "2026-10-16", "batch-c", "starting", "trunk agent is starting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vault, s := setup(t, tt.status)
			_, err := s.Send(context.Background(), vault, tt.date, tt.id)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want it to mention %q", err, tt.want)
			}
			assertNothingSent(t, s)
		})
	}
}

// A label must name exactly one workspace, and that workspace exactly one agent.
func TestSendRefusesAmbiguity(t *testing.T) {
	t.Parallel()
	const one = `{"result":{"workspaces":[{"workspace_id":"a","label":"trunk"}]}}`
	tests := []struct {
		name, spaces, agents, want string
	}{
		{"no workspace", `{"result":{"workspaces":[]}}`, `{"result":{"agents":[]}}`,
			`0 herdr workspaces labelled "trunk"`},
		{"two workspaces", `{"result":{"workspaces":[{"workspace_id":"a","label":"trunk"},{"workspace_id":"b","label":"trunk"}]}}`,
			`{"result":{"agents":[]}}`, `2 herdr workspaces labelled "trunk"`},
		{"no agent", one, `{"result":{"agents":[{"workspace_id":"b","pane_id":"b:1","agent_status":"idle"}]}}`,
			`0 agents in workspace "trunk"; expected one`},
		{"two agents", one, `{"result":{"agents":[{"workspace_id":"a","pane_id":"a:1","agent_status":"idle"},{"workspace_id":"a","pane_id":"a:2","agent_status":"idle"}]}}`,
			`2 agents in workspace "trunk"; expected one`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vault, s := setup(t, "idle")
			writeHerdr(t, s.Herdr, listing(tt.spaces, tt.agents, sentFile(s)))
			_, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c")
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err %v, want %q", err, tt.want)
			}
			assertNothingSent(t, s)
		})
	}
}

// herdr's own failure is reported with its stderr, else its stdout.
func TestHerdrFailure(t *testing.T) {
	t.Parallel()
	tests := []struct{ script, want string }{
		{"echo out; echo 'HERDR_ENV is not set' >&2; exit 1\n", "herdr workspace list: HERDR_ENV is not set"},
		{"echo ' only stdout '; exit 1\n", "herdr workspace list: only stdout"},
	}
	for _, tt := range tests {
		vault, s := setup(t, "idle")
		writeHerdr(t, s.Herdr, tt.script)
		if _, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c"); err == nil || err.Error() != tt.want {
			t.Errorf("err %v, want %q", err, tt.want)
		}
	}
}

// The child sees exactly the injected environment.
func TestHerdrEnv(t *testing.T) {
	t.Parallel()
	vault, s := setup(t, "idle")
	envFile := filepath.Join(filepath.Dir(s.Herdr), "env")
	writeHerdr(t, s.Herdr, "echo \"$QM_PROBE\" > '"+envFile+"'\nexit 1\n")
	s.Env = []string{"QM_PROBE=injected"}
	_, _ = s.Send(context.Background(), vault, "2026-10-16", "batch-c")
	if b, err := os.ReadFile(envFile); err != nil || string(b) != "injected\n" {
		t.Fatalf("env %q, %v", b, err)
	}
}

// dedent matches Python's textwrap.dedent, which the script applied to the block's body.
func TestDedent(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"a\n  b\n", "a\n  b\n"},
		{"  a\n    b\n", "a\n  b\n"},
		{"  a\n\n    b\n", "a\n\n  b\n"},
		{"  a\n \t\n  b", "a\n\nb"},
		{"\ta\n  b\n", "\ta\n  b\n"},
		{"\t a\n\t b\n", "a\nb\n"},
	}
	for _, tt := range tests {
		if got := dedent(tt.in); got != tt.want {
			t.Errorf("dedent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
