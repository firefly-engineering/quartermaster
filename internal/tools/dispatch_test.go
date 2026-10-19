package tools

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// TestTaskDispatchRefuses checks the input task_dispatch validates itself; internal/dispatch
// tests the dispatching.
func TestTaskDispatchRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in   TaskDispatchInput
		want string
	}{
		{TaskDispatchInput{Ref: "#12"}, "owner/repo#N"},
		{TaskDispatchInput{Ref: "acme/web#12", Mode: "later"}, "mode"},
		{TaskDispatchInput{Ref: "acme/web#12", Date: "today"}, "not a date"},
	} {
		if _, err := d.taskDispatch(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.want)
		}
	}
}

// TestTaskDispatchFreshRoutesByTier dispatches fresh items of each tier: [tiers] picks the
// harness the new agent starts as, and an item with no tier, or one [tiers] does not list,
// goes to default.
func TestTaskDispatchFreshRoutesByTier(t *testing.T) {
	t.Parallel()
	queue := "- Wave 2 after the stack lands.\n"
	items := "- ⬜ [#20](https://github.com/acme/web/issues/20): rename the store · Route: `/implement` · Tier: `refactoring` ^rename\n" +
		"- ⬜ [#21](https://github.com/acme/web/issues/21): fix the form · Route: `/implement` ^form\n" +
		"- ⬜ [#22](https://github.com/acme/web/issues/22): cache headers · Tier: `research` ^cache\n"
	for _, c := range []struct {
		ref, id, start string
	}{
		{"acme/web#20", "rename", "[agent][start][web-20][--kind][omp][--pane][w1:p9][--][--model][glm-5]\n"},
		{"acme/web#21", "form", "[agent][start][web-21][--kind][claude][--pane][w1:p9]\n"},
		{"acme/web#22", "cache", "[agent][start][web-22][--kind][claude][--pane][w1:p9]\n"},
	} {
		t.Run(c.ref, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, edited(t, queue, items+queue), true)
			h := newFakeHerdr(t)
			d.Herdr = h.Bin
			checkout := filepath.Join(t.TempDir(), "web")
			d.jj(t, "git", "init", checkout)
			d.Config.Dispatch.WorkspaceRoot = filepath.Join(t.TempDir(), "ws")
			d.Config.Projects.Repos = map[string]config.Project{"acme/web": {Path: checkout}}
			d.Config.Harness = map[string]config.Harness{
				"claude": {Kind: "claude"},
				"glm":    {Kind: "omp", Args: []string{"--model", "glm-5"}},
			}
			d.Config.Tiers = map[string]string{config.DefaultTier: "claude", "refactoring": "glm"}

			var out bytes.Buffer
			in := `{"ref":"` + c.ref + `","mode":"fresh","date":"` + fixtureDate + `"}`
			if err := RunVerb(t.Context(), All(d), "dispatch", []byte(in), &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"item": "`+c.id+`"`) || !strings.Contains(out.String(), `"pane": "w1:p9"`) {
				t.Errorf("output:\n%s", out.String())
			}
			if calls := h.calls(t); !strings.Contains(calls, c.start) {
				t.Errorf("herdr calls:\n%s\nlack %s", calls, c.start)
			}
			if got := d.note(t, fixtureDate); !strings.Contains(got, "<!-- qm pane=w1:p9 ws=") {
				t.Errorf("no dispatch marker on ^%s:\n%s", c.id, got)
			}
		})
	}
}

// TestTaskDispatchAgain dispatches a WAITING item fresh again, as escalation does (spec §9.2):
// its checkout is reused, and its previous worker's tab is closed first under refresh's guards,
// or the dispatch is refused while that worker runs.
func TestTaskDispatchAgain(t *testing.T) {
	t.Parallel()
	const tabClose, tabCreate = "[tab][close][w1:t1]\n", "[tab][create]"
	for _, c := range []struct {
		name    string
		marker  string                      // the item's marker; ws= is added when it has pane=
		setup   func(*testing.T, fakeHerdr) // herdr's state
		close   bool                        // tab close is called
		closed  string                      // the tab task_dispatch reports closed
		refused string                      // the error; "" when it dispatches
	}{
		{name: "idle", marker: "pane=w1:p1", close: true, closed: "w1:t1",
			setup: func(t *testing.T, h fakeHerdr) { h.agents(t, herdrAgent("omp", "web-20", "idle", "", 1)) }},
		{name: "done, its name counted", marker: "pane=w1:p1", close: true, closed: "w1:t1",
			setup: func(t *testing.T, h fakeHerdr) { h.agents(t, herdrAgent("omp", "web-20-2", "done", "", 1)) }},
		{name: "its agent exited", marker: "pane=w1:p1", close: true, closed: "w1:t1",
			setup: func(t *testing.T, h fakeHerdr) {
				h.panes(t, herdr.Pane{ID: "w1:p1", Workspace: "w1", Tab: "w1:t1", Status: "unknown"})
			}},
		{name: "its pane gone", marker: "pane=w1:p1", setup: func(*testing.T, fakeHerdr) {}},
		{name: "its tab gone as it closes", marker: "pane=w1:p1", close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-20", "idle", "", 1))
				h.fail(t, "tab close", `{"error":{"code":"tab_not_found","message":"tab w1:t1 not found"}}`)
			}},
		{name: "no pane recorded", marker: "", setup: func(*testing.T, fakeHerdr) {}},
		{name: "working", marker: "pane=w1:p1",
			setup:   func(t *testing.T, h fakeHerdr) { h.agents(t, herdrAgent("omp", "web-20", "working", "", 1)) },
			refused: "^cache: its worker is still running: web-20 in pane w1:p1 is working; dispatch it again once it is idle or done"},
		{name: "working, the project's herdr workspace gone", marker: "pane=w1:p1",
			setup: func(t *testing.T, h fakeHerdr) {
				h.workspaces(t)
				h.agents(t, herdrAgent("omp", "web-20", "working", "", 1))
			},
			refused: "^cache: its worker is still running: web-20 in pane w1:p1 is working; dispatch it again once it is idle or done"},
		{name: "blocked", marker: "pane=w1:p1",
			setup:   func(t *testing.T, h fakeHerdr) { h.agents(t, herdrAgent("omp", "web-20", "blocked", "", 1)) },
			refused: "^cache: its worker is still running: web-20 in pane w1:p1 is blocked; dispatch it again once it is idle or done"},
		{name: "renamed", marker: "pane=w1:p1",
			setup: func(t *testing.T, h fakeHerdr) { h.agents(t, herdrAgent("omp", "mine", "idle", "", 1)) },
			refused: `^cache: not dispatched again: its worker in pane w1:p1 is named "mine", not as its dispatch named it, ` +
				"so it keeps its tab"},
		{name: "the close fails", marker: "pane=w1:p1", close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-20", "idle", "", 1))
				h.fail(t, "tab close", "")
			},
			refused: "^cache: not dispatched again: closing tab w1:t1 of pane w1:p1: herdr tab close: tab close failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newFakeHerdr(t)
			d := fixtureDeps(t, settled(t), true)
			root := t.TempDir()
			ws := filepath.Join(root, "acme", "web", "issue-20")
			project := filepath.Join(t.TempDir(), "web")
			d.jj(t, "git", "init", project)
			if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
				t.Fatal(err)
			}
			d.jj(t, "-R", project, "workspace", "add", "--name", "issue-20", ws)
			if err := os.WriteFile(filepath.Join(ws, "attempt.txt"), []byte("the first worker's"), 0o644); err != nil {
				t.Fatal(err)
			}
			d.Herdr = h.Bin
			d.Config.Dispatch.WorkspaceRoot = root
			d.Config.Harness = map[string]config.Harness{"claude": {Kind: "claude"}}
			d.Config.Tiers = map[string]string{config.DefaultTier: "claude"}
			d.Config.Projects.Repos = map[string]config.Project{"acme/web": {Path: project}}
			marker := " <!-- qm ws=" + ws + " -->"
			if c.marker != "" {
				marker = " <!-- qm " + c.marker + " ws=" + ws + " -->"
			}
			line := "- " + string(note.IconWaiting) + " [#20](https://github.com/acme/web/issues/20): cache headers · Route: `/implement` · " +
				"Tier: `complex-implementation` — stuck: the cache layer is beyond me" + marker + " ^cache"
			src := edited(t, "- Wave 2 after the stack lands.", line)
			if err := os.WriteFile(filepath.Join(d.Config.Vault.Repo, "worklog", fixtureDate+".md"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			c.setup(t, h)

			out, err := d.taskDispatch(t.Context(), TaskDispatchInput{Date: fixtureDate, Ref: "acme/web#20", Mode: "fresh"})
			calls := h.calls(t)
			if closed := strings.Contains(calls, tabClose); closed != c.close {
				t.Errorf("tab closed: %v, want %v; calls:\n%s", closed, c.close, calls)
			}
			if c.refused != "" {
				if err == nil || err.Error() != c.refused {
					t.Errorf("err %v, want %q", err, c.refused)
				}
				if strings.Contains(calls, tabCreate) {
					t.Errorf("a worker started; calls:\n%s", calls)
				}
				if strings.Contains(calls, "[workspace][create]") {
					t.Errorf("a herdr workspace was created before the refusal; calls:\n%s", calls)
				}
				d.checkNote(t, src)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if i := strings.Index(calls, tabCreate); c.close && strings.Index(calls, tabClose) > i {
				t.Errorf("tab closed after the new one was made; calls:\n%s", calls)
			}
			if !out.Reused || out.Workspace != ws || out.Pane != "w1:p9" || out.Closed != c.closed {
				t.Errorf("output %+v", out)
			}
			if b, err := os.ReadFile(filepath.Join(ws, "attempt.txt")); err != nil || string(b) != "the first worker's" {
				t.Errorf("the checkout was not reused: %q %v", b, err)
			}
			n, err := note.Parse([]byte(d.note(t, fixtureDate)))
			if err != nil {
				t.Fatal(err)
			}
			if it := n.Item("cache"); it.Icon != note.IconInProgress || *it.Dispatch != (note.Dispatch{Pane: "w1:p9", Workspace: ws}) {
				t.Errorf("item %+v, its marker %+v", it, it.Dispatch)
			}
		})
	}
}

// batch is a batch work item in the fixture's Agent queue, after its list: it has no ref, and
// its prompt runs /mass-implement on two tickets.
const batch = "- Wave 2 after the stack lands.\n\n" +
	"##### ⬜ Wave 2: the form and the store\n\n" +
	"Tier: `basic-implementation`. ^wave-2\n\n" +
	"> [!prompt]- Prompt sent to web\n" +
	"> ```herdr-prompt id=wave-2 to=web\n" +
	"> /mass-implement acme/web #20 #21\n" +
	"> ```\n"

// TestTaskDispatchItemRefuses checks the item input: exactly one of ref and item, and a batch
// that can be started.
func TestTaskDispatchItemRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, edited(t, "- Wave 2 after the stack lands.\n", batch+
		"\n##### ⬜ Wave 3: no prompt yet\n\nTo plan. ^wave-3\n"+
		"\n##### ⬜ A batch named like a ref's checkout\n\nTo rename. ^issue-7\n\n"+
		"> [!prompt]- Prompt sent to web\n> ```herdr-prompt id=issue-7 to=web\n> /mass-implement acme/web #22\n> ```\n"), false)
	for _, c := range []struct {
		in   TaskDispatchInput
		want string
	}{
		{TaskDispatchInput{}, "give ref or item, not both"},
		{TaskDispatchInput{Ref: "acme/web#12", Item: "wave-2"}, "give ref or item, not both"},
		{TaskDispatchInput{Item: "Wave 2"}, "not an item id"},
		{TaskDispatchInput{Item: "wave-9", Date: fixtureDate}, "no work item ^wave-9"},
		{TaskDispatchInput{Item: "old-form", Date: fixtureDate}, "^old-form is DROPPED"},
		{TaskDispatchInput{Item: "wave-3", Date: fixtureDate}, "^wave-3 has no ref and no prompt"},
		{TaskDispatchInput{Item: "issue-7", Date: fixtureDate, Mode: "fresh"}, "cannot start with - or issue-"},
	} {
		if _, err := d.taskDispatch(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.want)
		}
	}
}

// TestTaskDispatchBatchSession sends a batch's prompt to its project's session, as any item's:
// to an available agent of the harness its tier routes to, whose pane the batch records.
func TestTaskDispatchBatchSession(t *testing.T) {
	t.Parallel()
	src := edited(t, "- Wave 2 after the stack lands.\n", batch)
	h := newFakeHerdr(t, mixed()...)
	d := sendDeps(t, src, h)

	var out bytes.Buffer
	in := `{"item":"wave-2","date":"` + fixtureDate + `"}`
	if err := RunVerb(t.Context(), All(d), "dispatch", []byte(in), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, `"ref"`) || !strings.Contains(got, `"item": "wave-2"`) ||
		!strings.Contains(got, `"mode": "session"`) || !strings.Contains(got, `"pane": "w1:p2"`) ||
		!strings.Contains(got, `"committed": true`) {
		t.Errorf("output:\n%s", got)
	}
	if calls := h.calls(t); !strings.Contains(calls, "[agent][prompt][w1:p2][/mass-implement acme/web #20 #21\n") {
		t.Errorf("herdr calls:\n%s\nlack the batch's prompt to the glm agent", calls)
	}
	const id = "Tier: `basic-implementation`. ^wave-2"
	d.checkNote(t, strings.Replace(src, id, "Tier: `basic-implementation`. <!-- qm pane=w1:p2 --> ^wave-2", 1))
}

// TestTaskDispatchBatchFresh dispatches a batch fresh, in a jj project and in a git one: its
// checkout directory, jj workspace or git branch, and agent are named after its item id, and
// its agent gets the batch's prompt.
func TestTaskDispatchBatchFresh(t *testing.T) {
	t.Parallel()
	for _, vcs := range []string{"jj", "git"} {
		t.Run(vcs, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, edited(t, "- Wave 2 after the stack lands.\n", batch), true)
			h := newFakeHerdr(t)
			d.Herdr = h.Bin
			checkout := filepath.Join(t.TempDir(), "web")
			if vcs == "jj" {
				d.jj(t, "git", "init", checkout)
			} else {
				d.Git = gitRepo(t, d, checkout)
			}
			root := filepath.Join(t.TempDir(), "ws")
			d.Config.Dispatch.WorkspaceRoot = root
			d.Config.Projects.Repos = map[string]config.Project{"acme/web": {Path: checkout}}
			d.Config.Harness = map[string]config.Harness{
				"claude": {Kind: "claude"},
				"glm":    {Kind: "omp", Args: []string{"--model", "glm-5"}},
			}
			d.Config.Tiers = map[string]string{config.DefaultTier: "claude", "basic-implementation": "glm"}

			var out bytes.Buffer
			in := `{"item":"wave-2","mode":"fresh","date":"` + fixtureDate + `","instruction":"Keep it small."}`
			if err := RunVerb(t.Context(), All(d), "dispatch", []byte(in), &out); err != nil {
				t.Fatal(err)
			}
			ws := filepath.Join(root, "acme", "web", "wave-2")
			if got := out.String(); strings.Contains(got, `"ref"`) || !strings.Contains(got, `"item": "wave-2"`) ||
				!strings.Contains(got, `"workspace": "`+ws+`"`) {
				t.Errorf("output:\n%s", got)
			}
			calls := h.calls(t)
			for _, want := range []string{
				"[tab][create][--workspace][w1][--cwd][" + ws + "][--label][web-wave-2][--no-focus]\n",
				"[agent][start][web-wave-2][--kind][omp][--pane][w1:p9][--][--model][glm-5]\n",
				"[agent][prompt][w1:p9][/mass-implement acme/web #20 #21\n\nKeep it small.\n\n",
			} {
				if !strings.Contains(calls, want) {
					t.Errorf("herdr calls:\n%s\nlack %s", calls, want)
				}
			}
			if !strings.Contains(calls, "work item ^wave-2 in ") {
				t.Errorf("the prompt lacks its closing line:\n%s", calls)
			}
			if vcs == "jj" {
				if got := d.jj(t, "-R", checkout, "workspace", "list"); !strings.Contains(got, "wave-2:") {
					t.Errorf("jj workspaces:\n%s\nlack wave-2", got)
				}
			} else if b := strings.TrimSpace(git(t, d, ws, "branch", "--show-current")); b != "wave-2" {
				t.Errorf("branch %q, want wave-2", b)
			}
			got := d.note(t, fixtureDate)
			if !strings.Contains(got, "##### 🔄 Wave 2: the form and the store") ||
				!strings.Contains(got, "<!-- qm pane=w1:p9 ws="+ws+" -->") {
				t.Errorf("^wave-2 is not recorded as dispatched:\n%s", got)
			}
		})
	}
}

// gitRepo makes a git repository with one commit at dir, and returns git's path.
func gitRepo(t *testing.T, d Deps, dir string) string {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	d.Git = bin
	git(t, d, "", "init", "-q", dir)
	git(t, d, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	return bin
}

// git runs d's git in dir.
func git(t *testing.T, d Deps, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(d.Git, args...)
	cmd.Env, cmd.Dir = d.Env, dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
