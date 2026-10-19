package tools

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/refresh"
)

// ghFake answers gh's question about each ref, keyed `owner/repo#N`, with its JSON. A question
// it has no answer for fails the test.
func ghFake(t *testing.T, answers map[string]string) refresh.Runner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		var owner, name, number string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "owner="); ok {
				owner = v
			} else if v, ok := strings.CutPrefix(a, "name="); ok {
				name = v
			} else if v, ok := strings.CutPrefix(a, "number="); ok {
				number = v
			}
		}
		ref := owner + "/" + name + "#" + number
		out, ok := answers[ref]
		if !ok {
			t.Errorf("gh asked about %s", ref)
			return nil, errors.New("unexpected")
		}
		return []byte(out), nil
	}
}

// noHerdr fails the test if herdr is run.
func noHerdr(t *testing.T) func(context.Context, ...string) ([]byte, error) {
	return func(_ context.Context, args ...string) ([]byte, error) {
		t.Errorf("herdr %v", args)
		return nil, errors.New("unexpected")
	}
}

const (
	openIssue = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"Issue","state":"OPEN","url":"https://github.com/acme/web/issues/14"}}}}`
	mergedPR  = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","state":"MERGED","url":"https://github.com/acme/web/pull/12"}}}}`
	openPR    = `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","state":"OPEN","url":"https://github.com/acme/web/pull/12"}}}}`
)

// TestRefreshSettles refreshes a note that GitHub proves nothing about: the write still ticks
// the action paired with a ticked Next line and derives the icons, and commits that.
func TestRefreshSettles(t *testing.T) {
	t.Parallel()
	const nextLine = "1. [ ] Rotate the staging token · 5 min · [[#^rotate-token|details]]"
	d := fixtureDeps(t, strings.Replace(string(readFixture(t)), nextLine, strings.Replace(nextLine, "[ ]", "[x]", 1), 1), true)
	rec := refresh.Reconciler{GH: ghFake(t, map[string]string{"acme/web#14": openIssue, "acme/web#12": openPR}), Herdr: herdr.Client{Run: noHerdr(t)}}
	out, err := d.refreshWith(t.Context(), RefreshInput{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || !out.Committed || len(out.Applied) != 0 || len(out.SkippedItems) != 2 ||
		out.SkippedItems[0] != (refresh.Skipped{Item: "session-store", Ref: "acme/web#14", Reason: "acme/web#14 is open"}) ||
		out.SkippedItems[1] != (refresh.Skipped{Item: "login-stack", Ref: "acme/web#12", Reason: "acme/web#12 is open"}) {
		t.Errorf("out %+v", out)
	}
	d.checkNote(t, edited(t, nextLine, strings.Replace(nextLine, "[ ]", "[x]", 1),
		"- [ ] Rotate the staging token ^rotate-token", "- [x] Rotate the staging token ^rotate-token",
		"#### 🔄 Your actions", "#### ✅ Your actions"))
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
		"worklog(2026-10-16): refresh: no evidence of a change\n") {
		t.Errorf("commit %q", log)
	}
}

// TestRefreshMoves applies what GitHub proves, as one write.
func TestRefreshMoves(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), true)
	rec := refresh.Reconciler{GH: ghFake(t, map[string]string{"acme/web#14": openIssue, "acme/web#12": mergedPR}), Herdr: herdr.Client{Run: noHerdr(t)}}
	out, err := d.refreshWith(t.Context(), RefreshInput{Date: fixtureDate}, rec)
	if err != nil {
		t.Fatal(err)
	}
	want := refresh.Applied{Item: "login-stack", Ref: "acme/web#12", From: "IN_REVIEW", To: "DONE",
		Summary: "merged https://github.com/acme/web/pull/12"}
	if !out.Changed || !out.Committed || len(out.Applied) != 1 || out.Applied[0].Item != want.Item ||
		out.Applied[0].Summary != want.Summary || out.Applied[0].To != want.To || len(out.SkippedItems) != 1 {
		t.Errorf("out %+v", out)
	}
	d.checkNote(t, edited(t,
		"- 🔍 [#12](https://github.com/acme/web/pull/12) login stack, in review ^login-stack",
		"- ✅ [#12](https://github.com/acme/web/pull/12) login stack, in review — merged https://github.com/acme/web/pull/12 ^login-stack",
		"#### 🔄 Agent queue", "#### ✅ Agent queue"))
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
		"worklog(2026-10-16): refresh: ^login-stack IN_REVIEW to DONE\n") {
		t.Errorf("commit %q", log)
	}
}

// TestRefreshRefusesUnparseable never writes a note that does not parse.
func TestRefreshRefusesUnparseable(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t)+"\n##### ⬜ stray\n", false)
	if _, err := d.refreshWith(t.Context(), RefreshInput{}, refresh.Reconciler{GH: noHerdr(t), Herdr: herdr.Client{Run: noHerdr(t)}}); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("err %v", err)
	}
}

// The fixture's dropped item, as a fresh dispatch leaves it: its agent is web-13, in pane w1:p1
// of tab w1:t1 (herdrAgent's tab 1).
const (
	oldForm      = "- ❌ [#13](https://github.com/acme/web/issues/13) old form: dropped, superseded by #12 ^old-form"
	oldFormFresh = "- ❌ [#13](https://github.com/acme/web/issues/13) old form: dropped, superseded by #12 <!-- qm pane=w1:p1 ws=/ws/acme/web/issue-13 --> ^old-form"
	oldFormDone  = "- ❌ [#13](https://github.com/acme/web/issues/13) old form: dropped, superseded by #12 <!-- qm ws=/ws/acme/web/issue-13 --> ^old-form"
)

// TestRefreshClosesFinishedDispatch closes a DONE or DROPPED item's fresh dispatch tab once its
// agent is idle or done, or gone, and drops pane= from its marker; it leaves a running agent, a
// renamed one and a session's pane alone (spec §9.2, §11).
func TestRefreshClosesFinishedDispatch(t *testing.T) {
	t.Parallel()
	const tabClose = "[tab][close][w1:t1]\n"
	for _, c := range []struct {
		name   string
		marked string                      // the item's line before
		setup  func(*testing.T, fakeHerdr) // herdr's state
		want   string                      // the item's line after
		close  bool                        // tab close is called
		apply  *refresh.Applied            // what refresh reports applied; nil for nothing
		skip   string                      // what it reports skipped, "" for nothing
	}{
		{name: "idle", marked: oldFormFresh, want: oldFormDone, close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13", "idle", "/ws/acme/web/issue-13", 1))
			},
			apply: &refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DROPPED", To: "DROPPED", Pane: "w1:p1", Tab: "w1:t1"}},
		{name: "done, its name counted", marked: oldFormFresh, want: oldFormDone, close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13-2", "done", "/ws/acme/web/issue-13", 1))
			},
			apply: &refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DROPPED", To: "DROPPED", Pane: "w1:p1", Tab: "w1:t1"}},
		{name: "a DONE item", marked: strings.Replace(oldFormFresh, "❌", "✅", 1), want: strings.Replace(oldFormDone, "❌", "✅", 1), close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13", "idle", "/ws/acme/web/issue-13", 1))
			},
			apply: &refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DONE", To: "DONE", Pane: "w1:p1", Tab: "w1:t1"}},
		{name: "its agent exited", marked: oldFormFresh, want: oldFormDone, close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t)
				h.panes(t, herdr.Pane{ID: "w1:p1", Workspace: "w1", Tab: "w1:t1", Cwd: "/ws/acme/web/issue-13", Status: "unknown"})
			},
			apply: &refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DROPPED", To: "DROPPED", Pane: "w1:p1", Tab: "w1:t1"}},
		{name: "its pane gone", marked: oldFormFresh, want: oldFormDone,
			setup: func(t *testing.T, h fakeHerdr) { h.agents(t) },
			apply: &refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DROPPED", To: "DROPPED", Pane: "w1:p1"}},
		{name: "its tab gone as it closes", marked: oldFormFresh, want: oldFormDone, close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13", "idle", "/ws/acme/web/issue-13", 1))
				h.fail(t, "tab close", `{"error":{"code":"tab_not_found","message":"tab w1:t1 not found"}}`)
			},
			apply: &refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DROPPED", To: "DROPPED", Pane: "w1:p1"}},
		{name: "working", marked: oldFormFresh, want: oldFormFresh,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13", "working", "/ws/acme/web/issue-13", 1))
			},
			skip: "its worker web-13 in pane w1:p1 is working: its tab closes once it is idle or done"},
		{name: "blocked", marked: oldFormFresh, want: oldFormFresh,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13", "blocked", "/ws/acme/web/issue-13", 1))
			},
			skip: "its worker web-13 in pane w1:p1 is blocked: its tab closes once it is idle or done"},
		{name: "renamed", marked: oldFormFresh, want: oldFormFresh,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "mine", "idle", "/ws/acme/web/issue-13", 1))
			},
			skip: `its worker in pane w1:p1 is named "mine", not as its dispatch named it, so it keeps its tab`},
		{name: "a session's agent", marked: oldFormFresh, want: oldFormFresh,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("claude", "web-claude", "idle", "/src/web", 1))
			},
			skip: `its worker in pane w1:p1 is named "web-claude", not as its dispatch named it, so it keeps its tab`},
		{name: "a session send's pane", marked: strings.Replace(oldFormFresh, " ws=/ws/acme/web/issue-13", "", 1),
			want:  strings.Replace(oldFormFresh, " ws=/ws/acme/web/issue-13", "", 1),
			setup: func(t *testing.T, h fakeHerdr) { h.agents(t, herdrAgent("omp", "web-13", "idle", "/src/web", 1)) }},
		{name: "the close fails", marked: oldFormFresh, want: oldFormFresh, close: true,
			setup: func(t *testing.T, h fakeHerdr) {
				h.agents(t, herdrAgent("omp", "web-13", "idle", "/ws/acme/web/issue-13", 1))
				h.fail(t, "tab close", "")
			},
			skip: "closing tab w1:t1 of pane w1:p1: herdr tab close: tab close failed; retried next refresh"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newFakeHerdr(t)
			c.setup(t, h)
			d := fixtureDeps(t, edited(t, oldForm, c.marked), true)
			rec := refresh.Reconciler{GH: ghFake(t, map[string]string{"acme/web#14": openIssue, "acme/web#12": openPR}), Herdr: herdr.New(h.Bin, nil)}
			out, err := d.refreshWith(t.Context(), RefreshInput{}, rec)
			if err != nil {
				t.Fatal(err)
			}
			d.checkNote(t, edited(t, oldForm, c.want))
			if closed := strings.Contains(h.calls(t), tabClose); closed != c.close {
				t.Errorf("tab closed: %v, want %v; calls:\n%s", closed, c.close, h.calls(t))
			}
			var applied []refresh.Applied
			if c.apply != nil {
				applied = []refresh.Applied{*c.apply}
			}
			if !slices.EqualFunc(out.Applied, applied, func(a, b refresh.Applied) bool {
				return a.Item == b.Item && a.Ref == b.Ref && a.From == b.From && a.To == b.To && a.Pane == b.Pane && a.Tab == b.Tab
			}) {
				t.Errorf("applied %+v, want %+v", out.Applied, applied)
			}
			var skip string
			for _, s := range out.SkippedItems {
				if s.Item == "old-form" {
					skip = s.Reason
				}
			}
			if skip != c.skip {
				t.Errorf("skipped %q, want %q", skip, c.skip)
			}
			if c.apply != nil {
				headline := "^old-form pane w1:p1 dropped"
				if c.apply.Tab != "" {
					headline = "^old-form tab w1:t1 closed"
				}
				if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
					"worklog(2026-10-16): refresh: "+headline+"\n") {
					t.Errorf("commit %q", log)
				}
			}
		})
	}
}

// checkoutDeps is fixtureDeps with jj and git, and a project acme/web under projects.root: a
// jj repo, or a git one, with one commit. Its fresh checkout for issue 13 is ws, made as
// dispatch makes it, and the fixture's dropped item records it alone, its tab closed already.
func checkoutDeps(t *testing.T, vcs string) (d Deps, project, ws string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, ws = filepath.Join(root, "src", "acme", "web"), filepath.Join(root, "ws", "acme", "web", "issue-13")
	d = fixtureDeps(t, edited(t, oldForm, strings.Replace(oldForm, " ^old-form", " <!-- qm ws="+ws+" --> ^old-form", 1)), true)
	d.Config.Projects.Root = filepath.Join(root, "src")
	if d.Git, err = exec.LookPath("git"); err != nil {
		t.Fatal("git not found: run the tests in the dev shell")
	}
	for _, dir := range []string{project, filepath.Dir(ws)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if vcs == "jj" {
		d.jj(t, "git", "init", project)
		d.jj(t, "-R", project, "commit", "-m", "init")
		d.jj(t, "-R", project, "workspace", "add", "--name", "issue-13", ws)
	} else {
		d.git(t, "init", "-q", project)
		d.git(t, "-C", project, "commit", "-q", "--allow-empty", "-m", "init")
		d.git(t, "-C", project, "worktree", "add", "-q", "-b", "issue-13", ws)
	}
	return d, project, ws
}

func (d Deps) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(d.Git, append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Env = d.Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// refreshCheckout refreshes d, which GitHub proves nothing about and whose items herdr is never
// asked about, and returns what it reports for the dropped item: applied, or skipped and why.
func (d Deps) refreshCheckout(t *testing.T) (applied *refresh.Applied, skip string) {
	t.Helper()
	rec := d.reconciler()
	rec.GH, rec.Herdr = ghFake(t, map[string]string{"acme/web#14": openIssue, "acme/web#12": openPR}), herdr.Client{Run: noHerdr(t)}
	out, err := d.refreshWith(t.Context(), RefreshInput{}, rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range out.Applied {
		if a.Item == "old-form" {
			applied = &a
		}
	}
	for _, s := range out.SkippedItems {
		if s.Item == "old-form" {
			skip = s.Reason
		}
	}
	return applied, skip
}

// checkRemoved checks that a refresh removed ws and dropped it from the item, whose marker went
// with it, and committed that.
func (d Deps) checkRemoved(t *testing.T, applied *refresh.Applied, skip, ws string) {
	t.Helper()
	if want := (refresh.Applied{Item: "old-form", Ref: "acme/web#13", From: "DROPPED", To: "DROPPED", Workspace: ws}); applied == nil ||
		applied.Item != want.Item || applied.Ref != want.Ref || applied.From != want.From || applied.To != want.To ||
		applied.Pane != "" || applied.Workspace != want.Workspace || skip != "" {
		t.Errorf("applied %+v, skipped %q; want %+v", applied, skip, want)
	}
	d.checkNote(t, edited(t))
	if _, err := os.Lstat(ws); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s is still there: %v", ws, err)
	}
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
		"worklog(2026-10-16): refresh: ^old-form checkout removed\n") {
		t.Errorf("commit %q", log)
	}
}

// TestRefreshRemovesFinishedCheckout removes a DONE or DROPPED item's fresh checkout once its
// tab is closed: a jj workspace snapshotted, forgotten and deleted; a git worktree removed, never
// forced, so a dirty one stays until the next refresh finds it clean. Its issue-<N> branch
// stays (spec §9.2, §11).
func TestRefreshRemovesFinishedCheckout(t *testing.T) {
	t.Parallel()
	t.Run("jj", func(t *testing.T) {
		t.Parallel()
		d, project, ws := checkoutDeps(t, "jj")
		if err := os.WriteFile(filepath.Join(ws, "work.txt"), []byte("work\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		applied, skip := d.refreshCheckout(t)
		d.checkRemoved(t, applied, skip, ws)
		if list := d.jj(t, "-R", project, "workspace", "list", "-T", `name ++ "\n"`); list != "default\n" {
			t.Errorf("workspaces:\n%s", list)
		}
		// The snapshot kept the work no one committed.
		if got := d.jj(t, "-R", project, "log", "--no-graph", "-r", `files(root:"work.txt")`, "-T", `"found\n"`); got != "found\n" {
			t.Errorf("work.txt in %q", got)
		}
	})
	t.Run("jj, its directory gone", func(t *testing.T) {
		t.Parallel()
		d, project, ws := checkoutDeps(t, "jj")
		if err := os.RemoveAll(ws); err != nil {
			t.Fatal(err)
		}
		applied, skip := d.refreshCheckout(t)
		d.checkRemoved(t, applied, skip, ws)
		if list := d.jj(t, "-R", project, "--ignore-working-copy", "workspace", "list", "-T", `name ++ "\n"`); list != "default\n" {
			t.Errorf("workspaces:\n%s", list)
		}
	})
	t.Run("git, dirty then clean", func(t *testing.T) {
		t.Parallel()
		d, project, ws := checkoutDeps(t, "git")
		if err := os.WriteFile(filepath.Join(ws, "work.txt"), []byte("work\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		applied, skip := d.refreshCheckout(t)
		if applied != nil || !strings.HasPrefix(skip, "removing checkout "+ws+": git ") ||
			!strings.Contains(skip, "contains modified or untracked files") || !strings.HasSuffix(skip, "; retried next refresh") {
			t.Errorf("applied %+v, skipped %q", applied, skip)
		}
		d.checkNote(t, edited(t, oldForm, strings.Replace(oldForm, " ^old-form", " <!-- qm ws="+ws+" --> ^old-form", 1)))
		if _, err := os.Stat(filepath.Join(ws, "work.txt")); err != nil {
			t.Fatal(err)
		}
		// Committed, the work leaves the tree clean, and the next refresh removes it.
		d.git(t, "-C", ws, "add", "work.txt")
		d.git(t, "-C", ws, "commit", "-q", "-m", "work")
		applied, skip = d.refreshCheckout(t)
		d.checkRemoved(t, applied, skip, ws)
		if got := d.git(t, "-C", project, "show", "issue-13:work.txt"); got != "work\n" {
			t.Errorf("issue-13:work.txt %q", got)
		}
	})
	t.Run("git, its directory gone", func(t *testing.T) {
		t.Parallel()
		d, project, ws := checkoutDeps(t, "git")
		if err := os.RemoveAll(ws); err != nil {
			t.Fatal(err)
		}
		applied, skip := d.refreshCheckout(t)
		d.checkRemoved(t, applied, skip, ws)
		if list := d.git(t, "-C", project, "worktree", "list"); strings.Contains(list, ws) {
			t.Errorf("worktrees:\n%s", list)
		}
		d.git(t, "-C", project, "rev-parse", "--verify", "-q", "issue-13")
	})
}
