package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/note"
	"github.com/firefly-engineering/quartermaster/internal/rex"
	"github.com/firefly-engineering/quartermaster/internal/rex/rextest"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

const date = "2026-10-18"

const fence = "```"

// testNote holds a work item for each case: o/r#11 to dispatch, o/r#12 done, o/r#13 with a
// prompt to another workspace, o/g#5 in a git project, o/r#14 twice.
const testNote = "# Worklog — " + date + "\n" + `
## ⬜ o/r

### ⬜ What remains

#### ⬜ Agent queue

- ⬜ [r#11](https://github.com/o/r/issues/11): dispatch ^d11
- ✅ [r#12](https://github.com/o/r/issues/12): done already ^d12
- ⬜ [g#5](https://github.com/o/g/issues/5): in git ^g5
- ⬜ [r#14](https://github.com/o/r/issues/14): once ^d14a
- ⬜ [r#14](https://github.com/o/r/issues/14): twice ^d14b

##### ⬜ [r#13](https://github.com/o/r/issues/13): with a prompt

[▶ send to iac](obsidian://shell-commands/?vault=worklog&execute=abc&_date=` + date + `&_item=d13) ^d13

> [!prompt]- Prompt sent to iac
> ` + fence + `herdr-prompt id=d13 to=iac
> Please do it.
> ` + fence + `
`

// fixture is a vault holding testNote, a jj project o/r and a git project o/g, a workspace
// root, and a Dispatcher on them with a fake rex, whose new tab is w1/t9, its pane w1/p9.
type fixture struct {
	d         Dispatcher
	rex       *rextest.Fake
	jjRepo    string
	gitRepo   string
	root      string
	notePath  string
	jjBin     string
	gitBin    string
	toolchain []string // the env jj and git run with
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{rex: rextest.New(t), root: filepath.Join(t.TempDir(), "ws")}
	var err error
	if f.jjBin, err = exec.LookPath("jj"); err != nil {
		t.Fatal("jj not found: run the tests in the dev shell")
	}
	if f.gitBin, err = exec.LookPath("git"); err != nil {
		t.Fatal("git not found: run the tests in the dev shell")
	}
	home := t.TempDir()
	jjCfg, gitCfg := filepath.Join(home, "jj.toml"), filepath.Join(home, "gitconfig")
	write(t, jjCfg, "[user]\nname = \"Test\"\nemail = \"test@example.com\"\n")
	write(t, gitCfg, "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n")
	f.toolchain = []string{"HOME=" + home, "JJ_CONFIG=" + jjCfg, "GIT_CONFIG_GLOBAL=" + gitCfg, "GIT_CONFIG_NOSYSTEM=1"}

	vaultRepo := t.TempDir()
	f.notePath = filepath.Join(vaultRepo, "worklog", date+".md")
	write(t, f.notePath, testNote)

	f.jjRepo = filepath.Join(t.TempDir(), "r")
	f.run(t, f.jjBin, "git", "init", f.jjRepo)
	f.gitRepo = filepath.Join(t.TempDir(), "g")
	f.run(t, f.gitBin, "init", f.gitRepo)
	f.run(t, f.gitBin, "-C", f.gitRepo, "commit", "--allow-empty", "-m", "init")

	// An idle omp agent, of harness glm, in o/r's checkout and in o/iac's.
	f.agents(rex.Agent{Kind: "omp", Workspace: "w1", Tab: "w1/t1", Pane: "w1/p1", Cwd: f.jjRepo, Status: rex.Idle},
		rex.Agent{Kind: "omp", Workspace: "w2", Tab: "w2/t1", Pane: "w2/p1", Cwd: "/src/iac", Status: rex.Idle})
	f.d = Dispatcher{
		Config: config.Config{
			Dispatch: config.Dispatch{WorkspaceRoot: f.root},
			Harness:  map[string]config.Harness{"glm": {Kind: "omp", Args: []string{"--model", "glm-5"}}},
			Tiers:    map[string]string{config.DefaultTier: "glm"},
			// o/r's workspace is found by its label, r; o/g's and o/iac's are set.
			Projects: config.Projects{Repos: map[string]config.Project{
				"o/r":   {Path: f.jjRepo},
				"o/g":   {Path: f.gitRepo, Workspace: "r"},
				"o/iac": {Path: "/src/iac", Workspace: "iac"},
			}},
		},
		Engine: &engine.Engine{Repo: vaultRepo, Dir: "worklog"},
		Rex:    f.rex.Bin, JJ: f.jjBin, Git: f.gitBin, Env: f.toolchain,
	}
	return f
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// agents makes the fake's sessions w1, labelled r, and w2, labelled iac, holding agents.
func (f *fixture) agents(agents ...rex.Agent) {
	f.rex.Edit(func(s *rextest.State) {
		s.Sessions = nil
		s.Workspace("w1", "r")
		s.Workspace("w2", "iac")
		for _, a := range agents {
			s.Agent(a)
		}
	})
}

// run runs a real jj or git with the fixture's env, failing the test on error.
func (f *fixture) run(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = f.toolchain
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", filepath.Base(bin), args, err, out)
	}
	return string(out)
}

// started reports whether dispatch made anything in rex: finding a workspace only reads.
func (f *fixture) started() bool { return f.rex.Writes() != "" }

func (f *fixture) item(t *testing.T, id string) *note.Item {
	t.Helper()
	src, err := os.ReadFile(f.notePath)
	if err != nil {
		t.Fatal(err)
	}
	n, err := note.Parse(src)
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	it := n.Item(id)
	if it == nil {
		t.Fatalf("no ^%s in\n%s", id, src)
	}
	return it
}

func (f *fixture) noteSrc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.notePath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ref(t *testing.T, s string) note.Ref {
	t.Helper()
	r, err := note.ParseRef(s, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func same(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	return ra == rb
}

// freshCalls are the rex writes of a fresh dispatch of o/r#n into ws: its tab, its agent
// started, then its prompt.
func freshCalls(ws string, n int, prompt string) string {
	name := "r-" + strconv.Itoa(n)
	return "window new w1 " + name + " " + ws + "\n" +
		"label w1/p9 " + name + "\n" +
		"paste w1/p9 omp --model glm-5\nsend-key w1/p9 enter\n" +
		"paste w1/p9 " + prompt + "\nsend-key w1/p9 enter\n"
}

func TestFreshJJ(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "Keep it small.")
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	if res.Workspace != ws || res.Pane != "w1/p9" || res.Item != "d11" || res.Reused || res.Mode != Fresh {
		t.Errorf("result %+v", res)
	}
	if got := f.run(t, f.jjBin, "-R", f.jjRepo, "workspace", "root", "--name", "issue-11"); !same(t, strings.TrimSpace(got), ws) {
		t.Errorf("workspace issue-11 at %s, want %s", got, ws)
	}
	prompt := "/skill:implement https://github.com/o/r/issues/11\n\nKeep it small.\n\nWhen this is done, update the worklog with the " +
		"worklog skill: work item ^d11 in " + f.notePath + ". Record the outcome and refresh Next."
	if got, want := f.rex.Writes(), freshCalls(ws, 11, prompt); got != want {
		t.Errorf("rex writes:\n%s\nwant:\n%s", got, want)
	}
	it := f.item(t, "d11")
	if it.Icon != note.IconInProgress || it.Dispatch == nil || *it.Dispatch != (note.Dispatch{Pane: "w1/p9", Workspace: ws}) {
		t.Errorf("item %+v dispatch %+v", it, it.Dispatch)
	}
	if !strings.Contains(f.noteSrc(t), "<!-- qm pane=w1/p9 ws="+ws+" --> ^d11\n") {
		t.Errorf("note:\n%s", f.noteSrc(t))
	}
}

// TestFreshReuses dispatches the same ref again: the workspace is reused, and the item, already
// IN_PROGRESS, gets the new marker.
func TestFreshReuses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	if _, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, ""); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws, "work.txt"), "in flight")
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Reused || res.Workspace != ws {
		t.Errorf("result %+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "work.txt")); err != nil || string(b) != "in flight" {
		t.Errorf("the workspace was clobbered: %q %v", b, err)
	}
	if strings.Count(f.noteSrc(t), "<!-- qm ") != 1 || f.item(t, "d11").Icon != note.IconInProgress {
		t.Errorf("note:\n%s", f.noteSrc(t))
	}
}

func TestFreshRefusesAnotherWorkspace(t *testing.T) {
	t.Parallel()
	t.Run("the ref's jj workspace name elsewhere", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		elsewhere := filepath.Join(t.TempDir(), "mine")
		f.run(t, f.jjBin, "-R", f.jjRepo, "workspace", "add", "--name", "issue-11", elsewhere)
		_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
		if err == nil || !strings.Contains(err.Error(), filepath.Base(elsewhere)) {
			t.Fatalf("err %v, want the other workspace's path", err)
		}
		if f.started() || f.noteSrc(t) != testNote {
			t.Error("dispatch went on")
		}
	})
	t.Run("the ref's jj workspace, its directory gone", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ws := filepath.Join(f.root, "o", "r", "issue-11")
		if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
			t.Fatal(err)
		}
		f.run(t, f.jjBin, "-R", f.jjRepo, "workspace", "add", "--name", "issue-11", ws)
		if err := os.RemoveAll(ws); err != nil {
			t.Fatal(err)
		}
		_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
		if err == nil || !strings.Contains(err.Error(), "jj workspace forget issue-11") {
			t.Fatalf("err %v, want the way out", err)
		}
		if f.started() {
			t.Error("dispatch went on")
		}
	})
	t.Run("something else at the path", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ws := filepath.Join(f.root, "o", "r", "issue-11")
		write(t, filepath.Join(ws, "x"), "")
		_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
		if err == nil || !strings.Contains(err.Error(), ws) {
			t.Fatalf("err %v, want the path", err)
		}
		if f.started() {
			t.Error("dispatch went on")
		}
	})
	t.Run("the ref's git branch in another worktree", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		elsewhere := filepath.Join(t.TempDir(), "mine")
		f.run(t, f.gitBin, "-C", f.gitRepo, "worktree", "add", "-b", "issue-5", elsewhere)
		_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, "")
		if err == nil || !strings.Contains(err.Error(), filepath.Base(elsewhere)) {
			t.Fatalf("err %v, want the other worktree's path", err)
		}
		if f.started() {
			t.Error("dispatch went on")
		}
	})
}

func TestFreshGit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ws := filepath.Join(f.root, "o", "g", "issue-5")
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused || res.Workspace != ws {
		t.Errorf("result %+v", res)
	}
	if b := strings.TrimSpace(f.run(t, f.gitBin, "-C", ws, "branch", "--show-current")); b != "issue-5" {
		t.Errorf("branch %q", b)
	}
	if d := f.item(t, "g5").Dispatch; d == nil || d.Workspace != ws {
		t.Errorf("dispatch %+v", d)
	}
	// Reused as it is; then, its directory gone, pruned and added again on its branch.
	if res, err = f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, ""); err != nil || !res.Reused {
		t.Fatalf("again: %+v %v", res, err)
	}
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	if res, err = f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, ""); err != nil || res.Reused {
		t.Fatalf("after removal: %+v %v", res, err)
	}
	if b := strings.TrimSpace(f.run(t, f.gitBin, "-C", ws, "branch", "--show-current")); b != "issue-5" {
		t.Errorf("branch %q", b)
	}
}

// TestFreshNameTaken names the agent and its tab the first name no live agent has, while the
// workspace's directory and branch keep issue-<N>.
func TestFreshNameTaken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.agents(rex.Agent{Kind: "omp", Name: "g-5", Workspace: "w1", Tab: "w1/t1", Pane: "w1/p1", Status: rex.Idle},
		rex.Agent{Kind: "omp", Name: "r-5-2", Workspace: "w1", Tab: "w1/t2", Pane: "w1/p2", Status: rex.Idle},
		rex.Agent{Kind: "omp", Workspace: "w2", Tab: "w2/t1", Pane: "w2/p1", Status: rex.Idle})
	ws := filepath.Join(f.root, "o", "g", "issue-5")
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != ws {
		t.Errorf("result %+v", res)
	}
	calls := f.rex.Writes()
	if !strings.Contains(calls, "window new w1 g-5-2 ") || !strings.Contains(calls, "label w1/p9 g-5-2\n") {
		t.Errorf("rex writes:\n%s\nwant the tab and the agent named g-5-2", calls)
	}
	if b := strings.TrimSpace(f.run(t, f.gitBin, "-C", ws, "branch", "--show-current")); b != "issue-5" {
		t.Errorf("branch %q", b)
	}
}

// TestFreshRexFails leaves the workspace and the item as they were, and says so.
func TestFreshRexFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rex.Edit(func(s *rextest.State) { s.Fail = map[string]string{"start": ""} })
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err == nil || !strings.Contains(err.Error(), "rex api call block.event: start failed") || !strings.Contains(err.Error(), ws) || !strings.Contains(err.Error(), "w1/p9") {
		t.Fatalf("err %v, want rex's reason, the workspace and the pane", err)
	}
	if _, err := os.Stat(ws); err != nil {
		t.Errorf("workspace: %v", err)
	}
	if f.noteSrc(t) != testNote {
		t.Errorf("the note changed:\n%s", f.noteSrc(t))
	}
}

// TestFreshPromptFails names a failed prompt by rex's call, not by the prompt, and leaves the
// item where it was. rex's tests cover a prompt not taken, which fails the same way.
func TestFreshPromptFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rex.Edit(func(s *rextest.State) { s.Fail = map[string]string{"prompt": ""} })
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "Today.")
	if err == nil || !strings.Contains(err.Error(), "rex api call block.event: prompt failed") || !strings.Contains(err.Error(), ws) || !strings.Contains(err.Error(), "w1/p9") {
		t.Fatalf("err %v, want rex's reason, the workspace and the pane", err)
	}
	if strings.Contains(err.Error(), "Today.") {
		t.Errorf("err %v holds the prompt", err)
	}
	if !strings.Contains(err.Error(), "is not moved") {
		t.Errorf("err %v does not say the item stays", err)
	}
	if f.noteSrc(t) != testNote {
		t.Errorf("the note changed:\n%s", f.noteSrc(t))
	}
}

// TestFreshNoteFails reports the started agent's pane when the note cannot be written.
func TestFreshNoteFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.rex.Edit(func(s *rextest.State) {
		s.OnStart = &rextest.File{Path: f.notePath, Text: "# Worklog — " + date + "\n"}
	})
	_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err == nil || !strings.Contains(err.Error(), "w1/p9") || !strings.Contains(err.Error(), "not recorded") {
		t.Fatalf("err %v, want the pane", err)
	}
}

// TestSession sends to an available agent of the item's harness, as `worklog send` does, and
// records its pane on the item.
func TestSession(t *testing.T) {
	t.Parallel()
	t.Run("the item's prompt, to its workspace", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#13"), Session, "Today.")
		if err != nil {
			t.Fatal(err)
		}
		if res.Pane != "w2/p1" || res.To != "iac" || res.Item != "d13" || !res.Note.Changed {
			t.Errorf("result %+v", res)
		}
		want := "paste w2/p1 Please do it.\n\nToday.\n\nWhen this is done, update the " +
			"worklog with the worklog skill: work item ^d13 in " + f.notePath + ". Record the outcome and refresh Next.\nsend-key w2/p1 enter\n"
		if got := f.rex.Writes(); got != want {
			t.Errorf("rex writes:\n%s\nwant:\n%s", got, want)
		}
		if src := f.noteSrc(t); !strings.Contains(src, "_item=d13) <!-- qm pane=w2/p1 --> ^d13\n") || strings.Count(src, "<!-- qm ") != 1 {
			t.Errorf("note:\n%s", f.noteSrc(t))
		}
	})
	t.Run("/implement, to the project's session", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		for _, c := range []struct{ ref, what string }{{"o/r#11", "work item ^d11"}, {"o/r#40", "o/r#40"}} {
			f.rex.ClearCalls()
			res, err := f.d.Dispatch(context.Background(), date, ref(t, c.ref), "", "")
			if err != nil {
				t.Fatal(err)
			}
			if res.Pane != "w1/p1" || res.To != "r" || res.Mode != Session {
				t.Errorf("result %+v", res)
			}
			n := strings.TrimPrefix(c.ref, "o/r#")
			want := "paste w1/p1 /skill:implement https://github.com/o/r/issues/" + n +
				"\n\nWhen this is done, update the worklog with the worklog skill: " + c.what + " in " + f.notePath +
				". Record the outcome and refresh Next.\nsend-key w1/p1 enter\n"
			if got := f.rex.Writes(); got != want {
				t.Errorf("rex writes:\n%s\nwant:\n%s", got, want)
			}
		}
		if src := f.noteSrc(t); !strings.Contains(src, "dispatch <!-- qm pane=w1/p1 --> ^d11\n") || strings.Count(src, "<!-- qm ") != 1 {
			t.Errorf("note:\n%s", f.noteSrc(t))
		}
	})
	t.Run("no available agent spawns one", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agents(rex.Agent{Kind: "claude", Workspace: "w1", Tab: "w1/t1", Pane: "w1/p1", Cwd: f.jjRepo, Status: rex.Idle},
			rex.Agent{Kind: "omp", Name: "r-glm", Workspace: "w1", Tab: "w1/t2", Pane: "w1/p2", Cwd: f.jjRepo, Status: rex.Working},
			rex.Agent{Kind: "omp", Workspace: "w1", Tab: "w1/t3", Pane: "w1/p3", Cwd: f.root + "/o/r/issue-9", Status: rex.Idle})
		res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Session, "")
		if err != nil || res.Pane != "w1/p9" {
			t.Fatalf("%+v, %v", res, err)
		}
		if want := "window new w1 r-glm-2 " + f.jjRepo + "\nlabel w1/p9 r-glm-2\n" +
			"paste w1/p9 omp --model glm-5\nsend-key w1/p9 enter\npaste w1/p9 "; !strings.Contains(f.rex.Writes(), want) {
			t.Errorf("rex writes:\n%s\nwant:\n%s", f.rex.Writes(), want)
		}
		if src := f.noteSrc(t); !strings.Contains(src, "<!-- qm pane=w1/p9 --> ^d11\n") {
			t.Errorf("note:\n%s", src)
		}
	})
}

func TestRefuses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, c := range []struct {
		ref, mode, want string
	}{
		{"o/r#12", Fresh, "DONE"},
		{"o/r#12", Session, "DONE"},
		{"o/r#14", Session, "^d14a, ^d14b"},
		{"o/r#40", Fresh, "no work item"},
		{"x/y#1", Fresh, "x/y"},
		{"o/r#11", "later", "mode"},
	} {
		if _, err := f.d.Dispatch(context.Background(), date, ref(t, c.ref), c.mode, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s: %v, want %q", c.ref, c.mode, err, c.want)
		}
	}
	if _, err := f.d.Dispatch(context.Background(), "2026-10-17", ref(t, "o/r#11"), Fresh, ""); err == nil || !strings.Contains(err.Error(), "no note") {
		t.Errorf("no note: %v", err)
	}
	g := f.d
	g.Config.Tiers = nil
	if _, err := g.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, ""); err == nil || !strings.Contains(err.Error(), "no harness") {
		t.Errorf("no harness: %v", err)
	}
	// A workspace the marker cannot record is refused before it is made.
	g = f.d
	g.Config.Dispatch.WorkspaceRoot = filepath.Join(f.root, "Application Support")
	if _, err := g.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, ""); err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Errorf("a root with a space: %v", err)
	}
	if len(f.rex.Calls()) != 0 || f.noteSrc(t) != testNote {
		t.Error("a refused dispatch acted")
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Errorf("workspace root: %v", err)
	}
}

// TestProjectWorkspace resolves a project's workspace: the override, else the one workspace
// with a pane in the checkout, else the one labelled with the repo's name, else none, and it has
// no checkout to create one at. The tools tests cover creation.
func TestProjectWorkspace(t *testing.T) {
	t.Parallel()
	type pane struct{ ws, cwd string }
	for _, c := range []struct {
		name     string
		repo     string
		override config.Project
		panes    []pane
		want     string   // the workspace id; "" for an error
		errs     []string // what the error names
		calls    []string // every call made, when set
	}{
		{
			name:     "the override wins over a pane and the label",
			repo:     "o/r",
			override: config.Project{Workspace: "iac"},
			panes:    []pane{{"w3", "/src/o/r"}},
			want:     "w2",
			calls:    []string{"session.list"},
		},
		{
			name:     "an override labelling no workspace",
			repo:     "o/r",
			override: config.Project{Workspace: "gone"},
			errs:     []string{"no checkout of it at /src/o/r", `projects."o/r".path`},
		},
		{
			name:  "a pane in the checkout wins over the label",
			repo:  "o/r",
			panes: []pane{{"w2", "/src/o/other"}, {"w3", "/src/o/r"}},
			want:  "w3",
		},
		{
			name:  "a pane under the checkout, its path uncleaned",
			repo:  "o/r",
			panes: []pane{{"w3", "/src/o/r/./sub/../sub/"}},
			want:  "w3",
		},
		{
			name:  "panes of one workspace are one match",
			repo:  "o/r",
			panes: []pane{{"w3", "/src/o/r"}, {"w3", "/src/o/r/a"}},
			want:  "w3",
		},
		{
			name:  "a sibling sharing the checkout's prefix is not under it",
			repo:  "o/r",
			panes: []pane{{"w3", "/src/o/rc"}, {"w2", "/src/o"}, {"w2", "relative/src/o/r"}},
			want:  "w1",
		},
		{
			name:  "panes of two workspaces in the checkout",
			repo:  "o/r",
			panes: []pane{{"w2", "/src/o/r"}, {"w3", "/src/o/r/x"}},
			errs:  []string{`"iac" (w2)`, `"misc" (w3)`, "/src/o/r", `projects."o/r".workspace`},
		},
		{
			name:     "a path override is where panes are looked for",
			repo:     "o/r",
			override: config.Project{Path: "/elsewhere/r"},
			panes:    []pane{{"w3", "/src/o/r"}, {"w2", "/elsewhere/r"}},
			want:     "w2",
		},
		{
			name:  "no pane and no label",
			repo:  "o/nothing",
			panes: []pane{{"w3", "/src/o/r"}},
			errs:  []string{"no workspace for o/nothing", "no checkout of it at /src/o/nothing", "nothing is cloned"},
		},
		{
			name: "two workspaces labelled with the repo's name",
			repo: "o/dup",
			errs: []string{`2 workspaces labelled "dup"`, `projects."o/dup".workspace`},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := rextest.New(t)
			f.Edit(func(s *rextest.State) {
				for _, w := range [][2]string{{"w1", "r"}, {"w2", "iac"}, {"w3", "misc"}, {"w4", "dup"}, {"w5", "dup"}} {
					s.Workspace(w[0], w[1])
				}
				for i, p := range c.panes {
					n := strconv.Itoa(i + 1)
					s.Pane(rex.Pane{ID: p.ws + "/p" + n, Workspace: p.ws, Tab: p.ws + "/t" + n, Cwd: p.cwd})
				}
			})
			projects := config.Projects{Root: "/src", Repos: map[string]config.Project{c.repo: c.override}}
			got, _, err := send.ProjectWorkspace(context.Background(), f.Client(), projects, c.repo)
			if c.want == "" {
				if err == nil {
					t.Fatalf("got %+v, want an error", got)
				}
				for _, e := range c.errs {
					if !strings.Contains(err.Error(), e) {
						t.Errorf("error %q lacks %q", err, e)
					}
				}
				return
			}
			if err != nil || got.ID != c.want {
				t.Fatalf("got %+v %v, want %s", got, err, c.want)
			}
			if w := f.Writes(); w != "" {
				t.Errorf("rex written to:\n%s", w)
			}
			if c.calls != nil && !slices.Equal(f.Calls(), c.calls) {
				t.Errorf("rex calls %q, want %q", f.Calls(), c.calls)
			}
		})
	}
}

// TestFreshByConvention dispatches a ref of a project with no entry: its checkout is under
// projects.root, and its workspace the one with a pane there.
func TestFreshByConvention(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	root := t.TempDir()
	f.d.Config.Projects = config.Projects{Root: root}
	checkout := filepath.Join(root, "o", "r")
	if err := os.MkdirAll(filepath.Dir(checkout), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.jjRepo, checkout); err != nil {
		t.Fatal(err)
	}
	f.rex.Edit(func(s *rextest.State) {
		s.Pane(rex.Pane{ID: "w2/p5", Workspace: "w2", Tab: "w2/t5", Cwd: filepath.Join(checkout, "sub")})
	})
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.To != "iac" || !strings.Contains(f.rex.Writes(), "window new w2 ") {
		t.Errorf("result %+v, rex writes:\n%s", res, f.rex.Writes())
	}

	// A project with no checkout under the root is refused before rex is asked.
	f.rex.ClearCalls()
	_, err = f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, "")
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "o", "g")) {
		t.Fatalf("err %v, want the conventional path", err)
	}
	if calls := f.rex.Calls(); len(calls) != 0 {
		t.Errorf("rex was asked: %q", calls)
	}
}
