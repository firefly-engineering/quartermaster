package send

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
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
	// Hold ForkLock so no parallel fork inherits the write fd: golang/go#22315 (ETXTBSY).
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
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
// trunk (wZ) and IaC (wX), the sessions of projects o/trunk and o/iac, with a claude agent in
// each checkout, trunk's with status.
func setup(t *testing.T, status string) (vault string, s Sender) {
	t.Helper()
	vault = t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "2026-10-16.md"), []byte(testNote), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	s = Sender{
		Config: config.Config{
			Harness: map[string]config.Harness{"claude": {Kind: "claude"}, "glm": {Kind: "omp"}},
			Tiers:   map[string]string{config.DefaultTier: "claude", "basic-implementation": "glm"},
			Projects: config.Projects{Repos: map[string]config.Project{
				"o/trunk": {Path: "/src/trunk", Workspace: "trunk"},
				"o/iac":   {Path: "/src/iac", Workspace: "IaC"},
			}},
		},
		Engine: &engine.Engine{Repo: vault},
		Herdr:  filepath.Join(bin, "herdr"),
	}
	spaces := mustJSON(t, map[string]any{"result": map[string]any{"workspaces": []map[string]string{
		{"workspace_id": "wZ", "label": "trunk"}, {"workspace_id": "wX", "label": "IaC"},
	}}})
	agents := mustJSON(t, map[string]any{"result": map[string]any{"agents": []map[string]string{
		{"agent": "claude", "workspace_id": "wZ", "pane_id": "wZ:p6", "cwd": "/src/trunk", "agent_status": status},
		{"agent": "claude", "workspace_id": "wX", "pane_id": "wX:p9", "cwd": "/src/iac/sub", "agent_status": "idle"},
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
	if want := (Sent{ID: "batch-c", To: "trunk", Pane: "wZ:p6", Path: filepath.Join(vault, "2026-10-16.md")}); got.ID != want.ID ||
		got.To != want.To || got.Pane != want.Pane || got.Path != want.Path || !got.Note.Changed {
		t.Fatalf("got %+v, want %+v, recorded", got, want)
	}
	if b, err := os.ReadFile(got.Path); err != nil || !strings.Contains(string(b), "_item=batch-c) <!-- qm pane=wZ:p6 --> ^batch-c") {
		t.Fatalf("the note does not record the pane: %v\n%s", err, b)
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

// TestSendItemWithInstruction appends an instruction to the item's prompt, before the closing line.
func TestSendItemWithInstruction(t *testing.T) {
	t.Parallel()
	vault, s := setup(t, "idle")
	if _, err := s.SendItem(context.Background(), vault, "2026-10-16", "batch-c", "Skip #819."); err != nil {
		t.Fatal(err)
	}
	if out := sent(t, s); !strings.HasPrefix(out, "wZ:p6\n/mass-implement arbora-partners/trunk #871 #819\n\nSkip #819.\n\nWhen this is done, update the worklog") {
		t.Fatalf("sent %q", out)
	}
}

// TestDeliver sends a given text to a session's agent, closed with the worklog line about
// the ref it names, through the same checks as Send, and records no pane for a ref with no item.
func TestDeliver(t *testing.T) {
	t.Parallel()
	vault, s := setup(t, "idle")
	p := Prompt{To: Workspace{ID: "wX", Label: "IaC"}, Checkout: "/src/iac", Text: "/implement https://github.com/o/r/issues/3",
		Date: "2026-10-16", Ref: "o/r#3"}
	got, err := s.Deliver(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, "2026-10-16.md")
	if want := (Sent{ID: "o/r#3", To: "IaC", Pane: "wX:p9", Path: path}); got.ID != want.ID || got.To != want.To ||
		got.Pane != want.Pane || got.Path != want.Path || got.Note.Changed {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	want := "wX:p9\n/implement https://github.com/o/r/issues/3\n\nWhen this is done, update the worklog with the worklog skill: " +
		"o/r#3 in " + path + ". Record the outcome and refresh Next."
	if out := sent(t, s); out != want {
		t.Fatalf("sent %q", out)
	}
	_, s = setup(t, "blocked")
	p.To, p.Checkout = Workspace{ID: "wZ", Label: "trunk"}, "/src/trunk"
	if _, err := s.Deliver(context.Background(), p); err == nil || !strings.Contains(err.Error(), "no available agent of harness claude") {
		t.Fatalf("a blocked agent: %v", err)
	}
	for _, bad := range []Prompt{{To: Workspace{Label: "trunk"}, Text: "go"}, {To: p.To, Text: " "}, {To: p.To, Text: "go", Date: "2026-10-16"}} {
		if _, err := s.Deliver(context.Background(), bad); err == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
	assertNothingSent(t, s)
}

// TestSendKeepsRelativeIndent also finds the agent under its checkout, in a subdirectory.
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

// TestSendAvailableStatuses sends only to an idle or done agent: a working one would queue the
// prompt, and a blocked one could take it as an answer.
func TestSendAvailableStatuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status string
		ok     bool
	}{{"idle", true}, {"done", true}, {"working", false}, {"blocked", false}, {"unknown", false}} {
		t.Run(c.status, func(t *testing.T) {
			t.Parallel()
			vault, s := setup(t, c.status)
			_, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c")
			if c.ok && err != nil {
				t.Fatal(err)
			}
			if !c.ok {
				want := `no available agent of harness claude in herdr workspace "trunk": none of kind claude in /src/trunk is idle or done`
				if err == nil || err.Error() != want {
					t.Fatalf("err %v, want %q", err, want)
				}
				assertNothingSent(t, s)
			}
		})
	}
}

func TestSendRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, date, id, want string
	}{
		{"an id with shell syntax", "2026-10-16", "x;rm", "not an item id"},
		{"an uppercase id", "2026-10-16", "Batch-c", "not an item id"},
		{"an empty id", "2026-10-16", "", "not an item id"},
		{"a path for a date", "../etc", "batch-c", "not a date"},
		{"an impossible date", "2026-02-30", "batch-c", "not a date"},
		{"a date with no note", "2026-10-17", "batch-c", "no note"},
		{"an id with no item", "2026-10-16", "missing", "no herdr-prompt block"},
		{"an item with no prompt", "2026-10-16", "bare", "no herdr-prompt block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vault, s := setup(t, "idle")
			_, err := s.Send(context.Background(), vault, tt.date, tt.id)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want it to mention %q", err, tt.want)
			}
			assertNothingSent(t, s)
		})
	}
	vault, s := setup(t, "idle")
	if _, err := s.Send(context.Background(), t.TempDir(), "2026-10-16", "batch-c"); err == nil || !strings.Contains(err.Error(), "not the configured one") {
		t.Errorf("another vault: %v", err)
	}
	s.Config.Projects.Repos = nil
	if _, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c"); err == nil ||
		err.Error() != `no project's session is in herdr workspace "trunk": set projects."<owner>/<repo>".workspace = "trunk"` {
		t.Errorf("no project: %v", err)
	}
	assertNothingSent(t, s)
}

// A label must name exactly one workspace.
func TestSendRefusesAmbiguity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, spaces, want string
	}{
		{"no workspace", `{"result":{"workspaces":[]}}`, `0 herdr workspaces labelled "trunk"`},
		{"two workspaces", `{"result":{"workspaces":[{"workspace_id":"a","label":"trunk"},{"workspace_id":"b","label":"trunk"}]}}`,
			`2 herdr workspaces labelled "trunk"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vault, s := setup(t, "idle")
			writeHerdr(t, s.Herdr, listing(tt.spaces, `{"result":{"agents":[]}}`, sentFile(s)))
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

func TestWithin(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		root, dir string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/", true},
		{"/a/b/", "/a/b/c/d", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/a/b/../c", false},
		{"/a/b", "a/b", false},
		{"/a/b", "", false},
		{"/", "/x", true},
	} {
		if got := Within(c.root, c.dir); got != c.want {
			t.Errorf("Within(%q, %q) = %v, want %v", c.root, c.dir, got, c.want)
		}
	}
}
