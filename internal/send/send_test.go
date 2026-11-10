package send

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/note"
	"github.com/firefly-engineering/quartermaster/internal/rex"
	"github.com/firefly-engineering/quartermaster/internal/rex/rextest"
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

##### ⬜ Bare canary

^send-bare

> [!prompt]- Prompt sent to IaC
> ` + fence + `herdr-prompt id=send-bare to=IaC
> Please do it.
> ` + fence + `

##### ⬜ No prompt

Nothing to send. ^bare
`

// writeScript writes a shell script at path, standing in for rex where the test needs no
// session, only a run.
func writeScript(t *testing.T, path, script string) {
	t.Helper()
	// Hold ForkLock so no parallel fork inherits the write fd: golang/go#22315 (ETXTBSY).
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
}

// setup returns a vault holding the note and a Sender wired to a fake rex: workspaces trunk
// (wZ) and IaC (wX), the sessions of projects o/trunk and o/iac, with a claude agent in each
// checkout, trunk's with status. A spawn's tab is wZ/t9, its pane wZ/p9.
func setup(t *testing.T, status string) (vault string, s Sender, f *rextest.Fake) {
	t.Helper()
	vault = t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "2026-10-16.md"), []byte(testNote), 0o644); err != nil {
		t.Fatal(err)
	}
	f = rextest.New(t)
	f.Edit(func(st *rextest.State) {
		st.Workspace("wZ", "trunk")
		st.Workspace("wX", "IaC")
		st.Agent(rex.Agent{Kind: "claude", Workspace: "wZ", Tab: "wZ/t6", Pane: "wZ/p6", Cwd: "/src/trunk", Status: status})
		st.Agent(rex.Agent{Kind: "claude", Workspace: "wX", Tab: "wX/t9", Pane: "wX/p9", Cwd: "/src/iac/sub", Status: rex.Idle})
	})
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
		Rex:    f.Bin,
	}
	return vault, s, f
}

// sent is the last text pasted, after the pane it went to and a newline: the prompt.
func sent(t *testing.T, f *rextest.Fake) string {
	t.Helper()
	calls := f.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if rest, ok := strings.CutPrefix(calls[i], "paste "); ok {
			pane, text, _ := strings.Cut(rest, " ")
			return pane + "\n" + text
		}
	}
	t.Fatal("nothing was sent")
	return ""
}

func assertNothingSent(t *testing.T, f *rextest.Fake) {
	t.Helper()
	if w := f.Writes(); w != "" {
		t.Fatalf("rex was written to:\n%s", w)
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
	vault, s, f := setup(t, "idle")
	got, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Sent{ID: "batch-c", To: "trunk", Pane: "wZ/p6", Path: filepath.Join(vault, "2026-10-16.md")}); got.ID != want.ID ||
		got.To != want.To || got.Pane != want.Pane || got.Path != want.Path || !got.Note.Changed {
		t.Fatalf("got %+v, want %+v, recorded", got, want)
	}
	if b, err := os.ReadFile(got.Path); err != nil || !strings.Contains(string(b), "_item=batch-c) <!-- qm pane=wZ/p6 --> ^batch-c") {
		t.Fatalf("the note does not record the pane: %v\n%s", err, b)
	}
	if got.String() != "sent batch-c to trunk (wZ/p6)" {
		t.Fatalf("message %q", got.String())
	}
	out := sent(t, f)
	if !strings.HasPrefix(out, "wZ/p6\n/mass-implement arbora-partners/trunk #871 #819\n\nWhen this is done, update the worklog") {
		t.Fatalf("sent %q", out)
	}
	want := "^batch-c in " + filepath.Join(vault, "2026-10-16.md") + ". Record the outcome and refresh Next."
	if !strings.HasSuffix(out, want) {
		t.Fatalf("sent %q, want suffix %q", out, want)
	}
}

// TestSendPromptOnlyItem: a send on an item with a prompt and nothing else, as a vault
// without Shell commands settings renders it, records its pane and keeps the note parsing:
// the item's ▶ label is written in italics where its button would go, before the marker
// (#168).
func TestSendPromptOnlyItem(t *testing.T) {
	t.Parallel()
	vault, s, _ := setup(t, "idle")
	got, err := s.Send(context.Background(), vault, "2026-10-16", "send-bare")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pane != "wX/p9" {
		t.Fatalf("pane %q", got.Pane)
	}
	b, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "*send to IaC* <!-- qm pane=wX/p9 --> ^send-bare") {
		t.Fatalf("the pane is not recorded on the label:\n%s", b)
	}
	if _, err := note.Parse(b); err != nil {
		t.Fatalf("the note no longer parses: %v", err)
	}
}

// TestSendItemWithInstruction appends an instruction to the item's prompt, before the closing line.
func TestSendItemWithInstruction(t *testing.T) {
	t.Parallel()
	vault, s, f := setup(t, "idle")
	if _, err := s.SendItem(context.Background(), vault, "2026-10-16", "batch-c", "Skip #819."); err != nil {
		t.Fatal(err)
	}
	if out := sent(t, f); !strings.HasPrefix(out, "wZ/p6\n/mass-implement arbora-partners/trunk #871 #819\n\nSkip #819.\n\nWhen this is done, update the worklog") {
		t.Fatalf("sent %q", out)
	}
}

// TestDeliver sends a given text to a session's agent, closed with the worklog line about
// the ref it names, through the same checks as Send, and records no pane for a ref with no item.
func TestDeliver(t *testing.T) {
	t.Parallel()
	vault, s, f := setup(t, "idle")
	p := Prompt{
		To: Workspace{ID: "wX", Label: "IaC"}, Repo: "o/iac", Checkout: "/src/iac", Text: "/implement https://github.com/o/r/issues/3",
		Date: "2026-10-16", Ref: "o/r#3",
	}
	got, err := s.Deliver(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, "2026-10-16.md")
	if want := (Sent{ID: "o/r#3", To: "IaC", Pane: "wX/p9", Path: path}); got.ID != want.ID || got.To != want.To ||
		got.Pane != want.Pane || got.Path != want.Path || got.Note.Changed {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	want := "wX/p9\n/implement https://github.com/o/r/issues/3\n\nWhen this is done, update the worklog with the worklog skill: " +
		"o/r#3 in " + path + ". Record the outcome and refresh Next."
	if out := sent(t, f); out != want {
		t.Fatalf("sent %q", out)
	}
	_, s, _ = setup(t, "blocked")
	p.To, p.Repo, p.Checkout = Workspace{ID: "wZ", Label: "trunk"}, "o/trunk", "/src/trunk"
	if got, err := s.Deliver(context.Background(), p); err != nil || got.Pane != "wZ/p9" {
		t.Fatalf("a blocked agent: %+v, %v, want a spawn's pane", got, err)
	}
	_, s, f = setup(t, "idle")
	for _, bad := range []Prompt{
		{To: Workspace{Label: "trunk"}, Repo: p.Repo, Text: "go"},
		{To: p.To, Repo: p.Repo, Text: " "},
		{To: p.To, Text: "go"},
		{To: p.To, Repo: p.Repo, Text: "go", Date: "2026-10-16"},
	} {
		if _, err := s.Deliver(context.Background(), bad); err == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
	assertNothingSent(t, f)
}

// TestSendKeepsRelativeIndent also finds the agent under its checkout, in a subdirectory.
func TestSendKeepsRelativeIndent(t *testing.T) {
	t.Parallel()
	vault, s, f := setup(t, "idle")
	if _, err := s.Send(context.Background(), vault, "2026-10-16", "ask-iac"); err != nil {
		t.Fatal(err)
	}
	if out := sent(t, f); !strings.HasPrefix(out, "wX/p9\nPlease create the deleter SA.\n  Indented line.\nReply with links.\n\n") {
		t.Fatalf("sent %q", out)
	}
}

// TestSendAvailableStatuses sends only to an idle or done agent: a working one would queue the
// prompt, a blocked one could take it as an answer, and one that reports nothing could be on
// any screen, so a new agent gets it instead.
func TestSendAvailableStatuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, status, pane string
	}{
		{"idle", "idle", "wZ/p6"},
		{"done", "done", "wZ/p6"},
		{"working", "working", "wZ/p9"},
		{"blocked", "blocked", "wZ/p9"},
		{"no report", "", "wZ/p9"},
		{"an unknown status", "unknown", "wZ/p9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			vault, s, f := setup(t, c.status)
			if _, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c"); err != nil {
				t.Fatal(err)
			}
			if out := sent(t, f); !strings.HasPrefix(out, c.pane+"\n") {
				t.Fatalf("sent %q, want to %s", out, c.pane)
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
		{"an id with no item", "2026-10-16", "missing", "no prompt block"},
		{"an item with no prompt", "2026-10-16", "bare", "no prompt block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vault, s, f := setup(t, "idle")
			_, err := s.Send(context.Background(), vault, tt.date, tt.id)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want it to mention %q", err, tt.want)
			}
			assertNothingSent(t, f)
		})
	}
	vault, s, f := setup(t, "idle")
	if _, err := s.Send(context.Background(), t.TempDir(), "2026-10-16", "batch-c"); err == nil || !strings.Contains(err.Error(), "not the configured one") {
		t.Errorf("another vault: %v", err)
	}
	s.Config.Projects.Repos = nil
	if _, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c"); err == nil ||
		err.Error() != `no project's session is in workspace "trunk": set projects."<owner>/<repo>".workspace = "trunk"` {
		t.Errorf("no project: %v", err)
	}
	assertNothingSent(t, f)
}

// A label must name exactly one workspace.
func TestSendRefusesAmbiguity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		spaces []rextest.Session
		want   string
	}{
		{"no workspace", []rextest.Session{}, `0 workspaces labelled "trunk"`},
		{"two workspaces", []rextest.Session{{ID: "a", Label: "trunk"}, {ID: "b", Label: "trunk"}}, `2 workspaces labelled "trunk"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vault, s, f := setup(t, "idle")
			f.Edit(func(st *rextest.State) { st.Sessions = tt.spaces })
			_, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c")
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err %v, want %q", err, tt.want)
			}
			assertNothingSent(t, f)
		})
	}
}

// rex's own failure is reported with its stderr, else its stdout.
func TestRexFailure(t *testing.T) {
	t.Parallel()
	tests := []struct{ script, want string }{
		{"echo out; echo 'no rex server running' >&2; exit 1\n", "rex api call session.list: no rex server running"},
		{"echo ' only stdout '; exit 1\n", "rex api call session.list: only stdout"},
	}
	for _, tt := range tests {
		vault, s, _ := setup(t, "idle")
		s.Rex = filepath.Join(t.TempDir(), "rex")
		writeScript(t, s.Rex, tt.script)
		if _, err := s.Send(context.Background(), vault, "2026-10-16", "batch-c"); err == nil || err.Error() != tt.want {
			t.Errorf("err %v, want %q", err, tt.want)
		}
	}
}

// The child sees exactly the injected environment.
func TestRexEnv(t *testing.T) {
	t.Parallel()
	vault, s, _ := setup(t, "idle")
	s.Rex = filepath.Join(t.TempDir(), "rex")
	envFile := filepath.Join(filepath.Dir(s.Rex), "env")
	writeScript(t, s.Rex, "echo \"$QM_PROBE\" > '"+envFile+"'\nexit 1\n")
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

func TestInCheckout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(real) // t.TempDir may itself sit under a symlink
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		root, dir string
		want      bool
	}{
		{link, filepath.Join(link, "sub"), true},
		{link, filepath.Join(real, "sub"), true},
		{link, filepath.Join(dir, "other"), false},
		{filepath.Join(dir, "absent"), filepath.Join(dir, "absent"), true},
	} {
		if got := InCheckout(c.root, c.dir); got != c.want {
			t.Errorf("InCheckout(%q, %q) = %v, want %v", c.root, c.dir, got, c.want)
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
