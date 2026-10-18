package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// monday is 2026-10-19, the Monday after the fixture's Friday.
var monday = time.Date(2026, 10, 19, 8, 30, 0, 0, time.UTC)

// newDeps makes a vault at <repo>/worklog holding files, keyed by name, and Deps on it with the
// clock stopped at now. With jj the repo is a jj repo.
func newDeps(t *testing.T, now time.Time, files map[string]string, jj bool) Deps {
	t.Helper()
	repo := t.TempDir()
	vault := filepath.Join(repo, "worklog")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		p := filepath.Join(vault, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := Deps{
		Config: config.Config{Vault: config.Vault{Repo: repo, Dir: "worklog", Name: "worklog"}},
		Now:    func() time.Time { return now },
	}
	if !jj {
		return d
	}
	bin, err := exec.LookPath("jj")
	if err != nil {
		t.Fatal("jj not found: run the tests in the dev shell")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, "jj.toml")
	if err := os.WriteFile(cfg, []byte("[user]\nname = \"Test\"\nemail = \"test@example.com\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.JJ, d.Env = bin, []string{"HOME=" + home, "JJ_CONFIG=" + cfg}
	d.jj(t, "git", "init", repo)
	d.jj(t, "-R", repo, "commit", "-m", "init")
	return d
}

func (d Deps) jj(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(d.JJ, args...)
	cmd.Env, cmd.Dir = d.Env, d.Config.Vault.Repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jj %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (d Deps) note(t *testing.T, date string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d.Config.Vault.Repo, "worklog", date+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDayOpenAfterWeekend opens Monday from Friday's note: the empty Sunday note, the later
// note and the undated file are not the previous note.
func TestDayOpenAfterWeekend(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, map[string]string{
		"2026-10-15.md": "# Worklog — 2026-10-15\n",
		"2026-10-16.md": string(readFixture(t)),
		"2026-10-18.md": "\n \n",
		"2026-10-20.md": "# Worklog — 2026-10-20\n",
		"ideas.md":      "# Ideas\n",
	}, true)
	out, err := d.dayOpen(t.Context(), DayOpenInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Date != "2026-10-19" || out.Previous != "2026-10-16" || !out.Committed {
		t.Fatalf("got %+v", out)
	}
	if len(out.CarryOver) != 13 || out.CarryOver[0].Item != "Ship the login page (acme/web)" {
		t.Fatalf("carry-over %+v", out.CarryOver)
	}
	if out.Status.Label != "open" || out.Status.Time != "08:30" ||
		!strings.Contains(out.Status.Text, "from 2026-10-16: 13 carry-over rows") {
		t.Fatalf("status %+v", out.Status)
	}
	src := d.note(t, "2026-10-19")
	n, err := note.Parse([]byte(src))
	if err != nil {
		t.Fatalf("the new note does not parse: %v\n%s", err, src)
	}
	if n.Title.Date != "2026-10-19" || len(n.CarryOver.Rows) != 13 || n.CarryOver.Rows[0].Verdict != "" {
		t.Fatalf("note:\n%s", src)
	}
	if !strings.Contains(src, "previous: \"[[2026-10-16]]\"") || !strings.Contains(src, "From [[2026-10-16]].") {
		t.Fatalf("the note does not name its previous one:\n%s", src)
	}
	log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description")
	if !strings.HasPrefix(log, "worklog(2026-10-19): day_open: open from 2026-10-16, 13 carry-over rows") {
		t.Fatalf("commit %q", log)
	}
}

func TestDayOpenNoPrevious(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, nil, false)
	out, err := d.dayOpen(t.Context(), DayOpenInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Previous != "" || len(out.CarryOver) != 0 || out.CarryOver == nil || out.Committed || out.Skipped == "" {
		t.Fatalf("got %+v", out)
	}
	if src := d.note(t, "2026-10-19"); strings.Contains(src, "previous:") {
		t.Fatalf("note names a previous one:\n%s", src)
	}
}

// TestDayOpenEmptyNote treats an existing empty or whitespace-only note as missing.
func TestDayOpenEmptyNote(t *testing.T) {
	t.Parallel()
	for name, empty := range map[string]string{"empty": "", "whitespace": "\n \n"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := newDeps(t, monday, map[string]string{
				"2026-10-16.md": string(readFixture(t)),
				"2026-10-19.md": empty,
			}, false)
			out, err := d.dayOpen(t.Context(), DayOpenInput{})
			if err != nil {
				t.Fatal(err)
			}
			if out.Previous != "2026-10-16" || !strings.HasPrefix(d.note(t, "2026-10-19"), "---\ndate: 2026-10-19\n") {
				t.Fatalf("got %+v", out)
			}
		})
	}
}

func TestDayOpenRefusesNonEmptyNote(t *testing.T) {
	t.Parallel()
	const today = "# Worklog — 2026-10-19\n\n**08:00 UTC:** opened by hand.\n"
	d := newDeps(t, monday, map[string]string{"2026-10-19.md": today}, false)
	_, err := d.dayOpen(t.Context(), DayOpenInput{})
	if err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("err %v, want a refusal", err)
	}
	if got := d.note(t, "2026-10-19"); got != today {
		t.Fatalf("note changed:\n%s", got)
	}
}

func TestDayOpenRefusesUnparseablePrevious(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, map[string]string{"2026-10-16.md": "stray\n\n# Worklog — 2026-10-16\n"}, false)
	if _, err := d.dayOpen(t.Context(), DayOpenInput{}); err == nil || !strings.Contains(err.Error(), "2026-10-16.md does not parse") {
		t.Fatalf("err %v", err)
	}
}

func TestNoVault(t *testing.T) {
	t.Parallel()
	d := Deps{Now: func() time.Time { return monday }}
	if _, err := d.dayOpen(t.Context(), DayOpenInput{}); err == nil || !strings.Contains(err.Error(), "vault.repo") {
		t.Fatalf("day_open: %v", err)
	}
	if _, err := d.dayRead(t.Context(), DayReadInput{}); err == nil || !strings.Contains(err.Error(), "vault.repo") {
		t.Fatalf("day_read: %v", err)
	}
}

func TestDayRead(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, map[string]string{"2026-10-16.md": string(readFixture(t))}, false)
	out, err := d.dayRead(t.Context(), DayReadInput{Date: "2026-10-16"})
	if err != nil {
		t.Fatal(err)
	}
	v := out.Note
	if len(out.Errors) != 0 || v.Date != "2026-10-16" || v.Status == nil || v.Status.Label != "close" {
		t.Fatalf("got %+v", out)
	}
	if len(v.Objectives) != 4 || v.Objectives[0].State != "IN_REVIEW" || len(v.Next) != 3 || v.Next[0].Box != "unchecked" ||
		v.Next[1].Ref != "login-stack" {
		t.Fatalf("objectives %+v, next %+v", v.Objectives, v.Next)
	}
	if len(v.Projects) != 2 || v.Projects[0].Repo != "acme/web" || len(v.Projects[0].Parts) != 1 {
		t.Fatalf("projects %+v", v.Projects)
	}
	decisions := v.Projects[0].Remains.Buckets[0]
	var titles []string
	for _, it := range decisions.Items {
		titles = append(titles, it.ID+" "+it.State)
	}
	if want := []string{"session-store TODO", " TODO", " DONE"}; !slices.Equal(titles, want) {
		t.Fatalf("decisions %q, want %q", titles, want)
	}
	if p := decisions.Items[0].Prompt; p == nil || p.To != "web" {
		t.Fatalf("prompt %+v", p)
	}
	if len(v.CarryOver) != 5 || len(v.ManualActions) != 7 || len(v.Anchors) != 0 {
		t.Fatalf("carry-over %d, actions %d", len(v.CarryOver), len(v.ManualActions))
	}
	if len(v.Process) != 1 || v.Process[0].Text != "File a ticket for the slow CI cache" {
		t.Fatalf("process %+v", v.Process)
	}
}

// TestDayReadParseErrors returns what parsed, and the errors, never refusing.
func TestDayReadParseErrors(t *testing.T) {
	t.Parallel()
	src := "# Worklog — 2026-10-19\n\n**08:00 UTC:** ok.\n\n## Objectives\n\n" +
		"| Objective | Done when | Progress |\n|---|---|---|\n| A (o/r) | x | no icon |\n"
	d := newDeps(t, monday, map[string]string{"2026-10-19.md": src}, false)
	out, err := d.dayRead(t.Context(), DayReadInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Note.Status == nil || len(out.Errors) != 1 || out.Errors[0].Line != 9 {
		t.Fatalf("got %+v", out)
	}
}

func TestDayReadMissing(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, map[string]string{"2026-10-18.md": " \n"}, false)
	for _, date := range []string{"2026-10-17", "2026-10-18"} {
		if _, err := d.dayRead(t.Context(), DayReadInput{Date: date}); err == nil || !strings.Contains(err.Error(), "no note") {
			t.Fatalf("%s: %v", date, err)
		}
	}
	if _, err := d.dayRead(t.Context(), DayReadInput{Date: "../x"}); err == nil || !strings.Contains(err.Error(), "not a date") {
		t.Fatalf("bad date: %v", err)
	}
}

// TestMCP lists the tools and calls some over MCP, as a client does.
func TestMCP(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, map[string]string{"2026-10-16.md": string(readFixture(t))}, false)
	s := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	Register(s, All(d))
	cs := connect(t, s)

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if tl.OutputSchema == nil || tl.InputSchema == nil {
			t.Errorf("%s lacks an input or output schema", tl.Name)
		}
		if ro := tl.Annotations != nil && tl.Annotations.ReadOnlyHint; ro != (tl.Name == "day_read") {
			t.Errorf("%s read-only %v", tl.Name, ro)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"carryover_verdict", "day_open", "day_read", "item_transition", "item_upsert",
		"next_set", "objective_set", "refresh", "status_set"}) {
		t.Fatalf("tools %v", names)
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "day_open", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("day_open: %v %+v", err, res)
	}
	var opened DayOpenOutput
	if err := remarshal(res.StructuredContent, &opened); err != nil || len(opened.CarryOver) != 13 {
		t.Fatalf("day_open: %v %+v", err, opened)
	}

	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "day_read", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("day_read: %v %+v", err, res)
	}
	var read DayReadOutput
	if err := remarshal(res.StructuredContent, &read); err != nil || read.Note.Date != "2026-10-19" || len(read.Note.CarryOver) != 13 {
		t.Fatalf("day_read: %v %+v", err, read)
	}

	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "item_upsert", Arguments: map[string]any{
		"id": "flaky", "repo": "acme/web", "bucket": "Agent queue", "title": "flaky test", "ref": "#9",
	}})
	if err != nil || res.IsError {
		t.Fatalf("item_upsert: %v %+v", err, res)
	}
	var upserted ItemUpsertOutput
	if err := remarshal(res.StructuredContent, &upserted); err != nil || !upserted.Created || upserted.Item.ID != "flaky" ||
		!strings.HasSuffix(upserted.Path, "2026-10-19.md") {
		t.Fatalf("item_upsert: %v %+v", err, upserted)
	}
	// The schema refuses an input without its required fields.
	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "item_transition", Arguments: map[string]any{"item": "flaky"}})
	if err == nil && !res.IsError {
		t.Fatalf("item_transition without to: %+v", res)
	}

	// A second open is a tool error, not a protocol one.
	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "day_open", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("second day_open: %v %+v", err, res)
	}
	// Unknown input fields are refused.
	res, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "day_read", Arguments: map[string]any{"day": "x"}})
	if err != nil || !res.IsError {
		t.Fatalf("day_read with an unknown field: %v %+v", err, res)
	}
}

func connect(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func remarshal(from, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

// TestRunVerb runs the tools as `quartermaster note` verbs do.
func TestRunVerb(t *testing.T) {
	t.Parallel()
	d := newDeps(t, monday, map[string]string{"2026-10-16.md": string(readFixture(t))}, false)
	ts := All(d)
	if got := Verbs(ts); !slices.Equal(got, []string{"open", "read", "objective", "item", "transition", "next", "status", "verdict", "refresh"}) {
		t.Fatalf("verbs %v", got)
	}
	ctx := context.Background()
	var out bytes.Buffer
	if err := RunVerb(ctx, ts, "open", nil, &out); err != nil {
		t.Fatal(err)
	}
	var opened DayOpenOutput
	if err := json.Unmarshal(out.Bytes(), &opened); err != nil || opened.Previous != "2026-10-16" {
		t.Fatalf("open: %v %s", err, out.String())
	}
	out.Reset()
	if err := RunVerb(ctx, ts, "read", []byte(`{"date": "2026-10-16"}`), &out); err != nil {
		t.Fatal(err)
	}
	var read DayReadOutput
	if err := json.Unmarshal(out.Bytes(), &read); err != nil || read.Note.Date != "2026-10-16" {
		t.Fatalf("read: %v %s", err, out.String())
	}
	out.Reset()
	if err := RunVerb(ctx, ts, "status", []byte(`{"text": "Opened by hand.", "at": "08:00"}`), &out); err != nil {
		t.Fatal(err)
	}
	var status StatusSetOutput
	if err := json.Unmarshal(out.Bytes(), &status); err != nil || status.Status.Text != "**08:00 UTC:** Opened by hand." {
		t.Fatalf("status: %v %s", err, out.String())
	}
	for input, want := range map[string]string{
		`{"day": "x"}`: "unknown field",
		`{} {}`:        "one JSON object",
		`[1]`:          "cannot unmarshal",
	} {
		if err := RunVerb(ctx, ts, "read", []byte(input), &out); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", input, err, want)
		}
	}
	if err := RunVerb(ctx, ts, "close", nil, &out); !errors.Is(err, ErrUnknownVerb) {
		t.Fatalf("unknown verb: %v", err)
	}
}
