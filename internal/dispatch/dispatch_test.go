package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/note"
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

- ⬜ [#11](https://github.com/o/r/issues/11): dispatch ^d11
- ✅ [#12](https://github.com/o/r/issues/12): done already ^d12
- ⬜ [g#5](https://github.com/o/g/issues/5): in git ^g5
- ⬜ [#14](https://github.com/o/r/issues/14): once ^d14a
- ⬜ [#14](https://github.com/o/r/issues/14): twice ^d14b

##### ⬜ [#13](https://github.com/o/r/issues/13): with a prompt

[▶ send to iac](obsidian://shell-commands/?vault=worklog&execute=abc&_date=` + date + `&_item=d13) ^d13

> [!prompt]- Prompt sent to iac
> ` + fence + `herdr-prompt id=d13 to=iac
> Please do it.
> ` + fence + `
`

// fakeHerdr answers the herdr calls dispatch makes, logging each call's arguments as
// `[arg]` runs, one call per line. A file named fail-<verb> in its directory makes that call
// fail; a file named clobber makes `agent start` overwrite the note whose path it holds. Files
// named workspaces, panes and agents, when present, are the answers of `workspace list`,
// `pane list` and `agent list`; newFixture writes agents. Every pane `pane get` asks about is
// gone: a dispatch again finds its previous worker exited.
const fakeHerdr = `#!/bin/sh
dir=$(dirname "$0")
for a in "$@"; do printf '[%s]' "$a" >> "$dir/calls"; done
echo >> "$dir/calls"
if [ -e "$dir/fail-$2" ]; then echo "$2 failed" >&2; exit 1; fi
case "$1 $2" in
  "workspace list") if [ -e "$dir/workspaces" ]; then cat "$dir/workspaces"; else
    echo '{"result":{"workspaces":[{"workspace_id":"w1","label":"r"},{"workspace_id":"w2","label":"iac"}]}}'; fi ;;
  "pane list") if [ -e "$dir/panes" ]; then cat "$dir/panes"; else echo '{"result":{"panes":[]}}'; fi ;;
  "agent list") cat "$dir/agents" ;;
  "pane get") echo '{"error":{"code":"pane_not_found","message":"pane not found"}}' >&2; exit 1 ;;
  "tab create") echo '{"result":{"tab":{"tab_id":"w1:t5"},"root_pane":{"pane_id":"w1:p7"}}}' ;;
  "agent start") if [ -e "$dir/clobber" ]; then read -r p < "$dir/clobber"; printf '# Worklog — 2026-10-18\n' > "$p"; fi ;;
  "agent prompt") ;;
  *) echo "unexpected: $*" >&2; exit 3 ;;
esac
`

// fixture is a vault holding testNote, a jj project o/r and a git project o/g, a workspace
// root, and a Dispatcher on them with the fake herdr.
type fixture struct {
	d         Dispatcher
	bin       string // the fake herdr's directory
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
	f := &fixture{bin: t.TempDir(), root: filepath.Join(t.TempDir(), "ws")}
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

	herdr := filepath.Join(f.bin, "herdr")
	writeHerdr(t, herdr)
	// An idle omp agent, of harness glm, in o/r's checkout and in o/iac's.
	write(t, filepath.Join(f.bin, "agents"), `{"result":{"agents":[`+
		`{"agent":"omp","workspace_id":"w1","pane_id":"w1:p1","cwd":"`+f.jjRepo+`","agent_status":"idle"},`+
		`{"agent":"omp","workspace_id":"w2","pane_id":"w2:p1","cwd":"/src/iac","agent_status":"idle"}]}}`)
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
		Herdr:  herdr, JJ: f.jjBin, Git: f.gitBin, Env: f.toolchain,
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

// writeHerdr writes the fakeHerdr stand-in at path.
func writeHerdr(t *testing.T, path string) {
	t.Helper()
	// Hold ForkLock so no parallel fork inherits the write fd: golang/go#22315 (ETXTBSY).
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte(fakeHerdr), 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
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

// calls are the fake herdr's calls so far, one per line.
func (f *fixture) calls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.bin, "calls"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// started reports whether dispatch made anything in herdr: finding a workspace only reads.
func (f *fixture) started(t *testing.T) bool {
	t.Helper()
	return strings.Contains(f.calls(t), "[tab]")
}

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

// freshCalls are the herdr calls of a fresh dispatch of o/r#n into ws, then its prompt.
func freshCalls(ws string, n int, prompt string) string {
	return "[workspace][list]\n[pane][list]\n[agent][list]\n" +
		"[tab][create][--workspace][w1][--cwd][" + ws + "][--label][r-" + strconv.Itoa(n) + "][--no-focus]\n" +
		"[agent][start][r-" + strconv.Itoa(n) + "][--kind][omp][--pane][w1:p7][--][--model][glm-5]\n" +
		"[agent][prompt][w1:p7][" + prompt + "]\n"
}

func TestFreshJJ(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "Keep it small.")
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	if res.Workspace != ws || res.Pane != "w1:p7" || res.Item != "d11" || res.Reused || res.Mode != Fresh {
		t.Errorf("result %+v", res)
	}
	if got := f.run(t, f.jjBin, "-R", f.jjRepo, "workspace", "root", "--name", "issue-11"); !same(t, strings.TrimSpace(got), ws) {
		t.Errorf("workspace issue-11 at %s, want %s", got, ws)
	}
	prompt := "/skill:implement https://github.com/o/r/issues/11\n\nKeep it small.\n\nWhen this is done, update the worklog with the " +
		"worklog skill: work item ^d11 in " + f.notePath + ". Record the outcome and refresh Next."
	if got, want := f.calls(t), freshCalls(ws, 11, prompt); got != want {
		t.Errorf("herdr calls:\n%s\nwant:\n%s", got, want)
	}
	it := f.item(t, "d11")
	if it.Icon != note.IconInProgress || it.Dispatch == nil || *it.Dispatch != (note.Dispatch{Pane: "w1:p7", Workspace: ws}) {
		t.Errorf("item %+v dispatch %+v", it, it.Dispatch)
	}
	if !strings.Contains(f.noteSrc(t), "<!-- qm pane=w1:p7 ws="+ws+" --> ^d11\n") {
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
		if f.started(t) || f.noteSrc(t) != testNote {
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
		if f.started(t) {
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
		if f.started(t) {
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
		if f.started(t) {
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
	write(t, filepath.Join(f.bin, "agents"), `{"result":{"agents":[`+
		`{"name":"g-5","workspace_id":"w1","pane_id":"w1:p1","agent_status":"idle"},`+
		`{"name":"r-5-2","workspace_id":"w1","pane_id":"w1:p2","agent_status":"idle"},`+
		`{"workspace_id":"w2","pane_id":"w2:p1","agent_status":"idle"}]}}`)
	ws := filepath.Join(f.root, "o", "g", "issue-5")
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != ws {
		t.Errorf("result %+v", res)
	}
	calls := f.calls(t)
	if !strings.Contains(calls, "[--label][g-5-2][--no-focus]\n") || !strings.Contains(calls, "[agent][start][g-5-2][--kind]") {
		t.Errorf("herdr calls:\n%s\nwant the tab and the agent named g-5-2", calls)
	}
	if b := strings.TrimSpace(f.run(t, f.gitBin, "-C", ws, "branch", "--show-current")); b != "issue-5" {
		t.Errorf("branch %q", b)
	}
}

// TestFreshHerdrFails leaves the workspace and the item as they were, and says so.
func TestFreshHerdrFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, filepath.Join(f.bin, "fail-start"), "")
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err == nil || !strings.Contains(err.Error(), "herdr agent start: start failed") || !strings.Contains(err.Error(), ws) || !strings.Contains(err.Error(), "w1:p7") {
		t.Fatalf("err %v, want herdr's reason, the workspace and the pane", err)
	}
	if _, err := os.Stat(ws); err != nil {
		t.Errorf("workspace: %v", err)
	}
	if f.noteSrc(t) != testNote {
		t.Errorf("the note changed:\n%s", f.noteSrc(t))
	}
}

// TestFreshPromptFails names the failed `agent prompt` by herdr's verb, not by the prompt.
func TestFreshPromptFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, filepath.Join(f.bin, "fail-prompt"), "")
	ws := filepath.Join(f.root, "o", "r", "issue-11")
	_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "Today.")
	if err == nil || !strings.Contains(err.Error(), "herdr agent prompt: prompt failed") || !strings.Contains(err.Error(), ws) || !strings.Contains(err.Error(), "w1:p7") {
		t.Fatalf("err %v, want herdr's reason, the workspace and the pane", err)
	}
	if strings.Contains(err.Error(), "Today.") {
		t.Errorf("err %v holds the prompt", err)
	}
}

// TestFreshNoteFails reports the started agent's pane when the note cannot be written.
func TestFreshNoteFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, filepath.Join(f.bin, "clobber"), f.notePath+"\n")
	_, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err == nil || !strings.Contains(err.Error(), "w1:p7") || !strings.Contains(err.Error(), "not recorded") {
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
		if res.Pane != "w2:p1" || res.To != "iac" || res.Item != "d13" || !res.Note.Changed {
			t.Errorf("result %+v", res)
		}
		want := "[workspace][list]\n[agent][list]\n[agent][prompt][w2:p1][Please do it.\n\nToday.\n\nWhen this is done, update the " +
			"worklog with the worklog skill: work item ^d13 in " + f.notePath + ". Record the outcome and refresh Next.]\n"
		if got := f.calls(t); got != want {
			t.Errorf("herdr calls:\n%s\nwant:\n%s", got, want)
		}
		if src := f.noteSrc(t); !strings.Contains(src, "_item=d13) <!-- qm pane=w2:p1 --> ^d13\n") || strings.Count(src, "<!-- qm ") != 1 {
			t.Errorf("note:\n%s", f.noteSrc(t))
		}
	})
	t.Run("/implement, to the project's session", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		for _, c := range []struct{ ref, what string }{{"o/r#11", "work item ^d11"}, {"o/r#40", "o/r#40"}} {
			if err := os.Remove(filepath.Join(f.bin, "calls")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			res, err := f.d.Dispatch(context.Background(), date, ref(t, c.ref), "", "")
			if err != nil {
				t.Fatal(err)
			}
			if res.Pane != "w1:p1" || res.To != "r" || res.Mode != Session {
				t.Errorf("result %+v", res)
			}
			n := strings.TrimPrefix(c.ref, "o/r#")
			want := "[workspace][list]\n[pane][list]\n[agent][list]\n[agent][prompt][w1:p1][/skill:implement https://github.com/o/r/issues/" + n +
				"\n\nWhen this is done, update the worklog with the worklog skill: " + c.what + " in " + f.notePath +
				". Record the outcome and refresh Next.]\n"
			if got := f.calls(t); got != want {
				t.Errorf("herdr calls:\n%s\nwant:\n%s", got, want)
			}
		}
		if src := f.noteSrc(t); !strings.Contains(src, "dispatch <!-- qm pane=w1:p1 --> ^d11\n") || strings.Count(src, "<!-- qm ") != 1 {
			t.Errorf("note:\n%s", f.noteSrc(t))
		}
	})
	t.Run("no available agent spawns one", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		write(t, filepath.Join(f.bin, "agents"), `{"result":{"agents":[`+
			`{"agent":"claude","workspace_id":"w1","pane_id":"w1:p1","cwd":"`+f.jjRepo+`","agent_status":"idle"},`+
			`{"agent":"omp","name":"r-glm","workspace_id":"w1","pane_id":"w1:p2","cwd":"`+f.jjRepo+`","agent_status":"working"},`+
			`{"agent":"omp","workspace_id":"w1","pane_id":"w1:p3","cwd":"`+f.root+`/o/r/issue-9","agent_status":"idle"}]}}`)
		res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Session, "")
		if err != nil || res.Pane != "w1:p7" {
			t.Fatalf("%+v, %v", res, err)
		}
		if want := "[tab][create][--workspace][w1][--cwd][" + f.jjRepo + "][--label][r-glm-2][--no-focus]\n" +
			"[agent][start][r-glm-2][--kind][omp][--pane][w1:p7][--][--model][glm-5]\n[agent][prompt][w1:p7]["; !strings.Contains(f.calls(t), want) {
			t.Errorf("herdr calls:\n%s\nwant:\n%s", f.calls(t), want)
		}
		if src := f.noteSrc(t); !strings.Contains(src, "<!-- qm pane=w1:p7 --> ^d11\n") {
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
	if f.calls(t) != "" || f.noteSrc(t) != testNote {
		t.Error("a refused dispatch acted")
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Errorf("workspace root: %v", err)
	}
}

// TestHerdrWorkspace resolves a project's herdr workspace: the override, else the one
// workspace with a pane in the checkout, else the one labelled with the repo's name, else none,
// and it has no checkout to create one at. The tools tests cover creation.
func TestHerdrWorkspace(t *testing.T) {
	t.Parallel()
	const spaces = `{"result":{"workspaces":[` +
		`{"workspace_id":"w1","label":"r"},{"workspace_id":"w2","label":"iac"},` +
		`{"workspace_id":"w3","label":"misc"},{"workspace_id":"w4","label":"dup"},{"workspace_id":"w5","label":"dup"}]}}`
	pane := func(ws, cwd string) string { return `{"workspace_id":"` + ws + `","cwd":"` + cwd + `"}` }
	panes := func(ps ...string) string { return `{"result":{"panes":[` + strings.Join(ps, ",") + `]}}` }
	for _, c := range []struct {
		name     string
		repo     string
		override config.Project
		panes    string
		want     string   // the workspace id; "" for an error
		errs     []string // what the error names
		calls    string
	}{
		{
			name:     "the override wins over a pane and the label",
			repo:     "o/r",
			override: config.Project{Workspace: "iac"},
			panes:    panes(pane("w3", "/src/o/r")),
			want:     "w2",
			calls:    "[workspace][list]\n",
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
			panes: panes(pane("w2", "/src/o/other"), pane("w3", "/src/o/r")),
			want:  "w3",
			calls: "[workspace][list]\n[pane][list]\n",
		},
		{
			name:  "a pane under the checkout, its path uncleaned",
			repo:  "o/r",
			panes: panes(pane("w3", "/src/o/r/./sub/../sub/")),
			want:  "w3",
		},
		{
			name:  "panes of one workspace are one match",
			repo:  "o/r",
			panes: panes(pane("w3", "/src/o/r"), pane("w3", "/src/o/r/a")),
			want:  "w3",
		},
		{
			name:  "a sibling sharing the checkout's prefix is not under it",
			repo:  "o/r",
			panes: panes(pane("w3", "/src/o/rc"), pane("w2", "/src/o"), pane("w2", "relative/src/o/r")),
			want:  "w1",
		},
		{
			name:  "panes of two workspaces in the checkout",
			repo:  "o/r",
			panes: panes(pane("w2", "/src/o/r"), pane("w3", "/src/o/r/x")),
			errs:  []string{`"iac" (w2)`, `"misc" (w3)`, "/src/o/r", `projects."o/r".workspace`},
		},
		{
			name:     "a path override is where panes are looked for",
			repo:     "o/r",
			override: config.Project{Path: "/elsewhere/r"},
			panes:    panes(pane("w3", "/src/o/r"), pane("w2", "/elsewhere/r")),
			want:     "w2",
		},
		{
			name:  "no pane and no label",
			repo:  "o/nothing",
			panes: panes(pane("w3", "/src/o/r")),
			errs:  []string{"no herdr workspace for o/nothing", "no checkout of it at /src/o/nothing", "nothing is cloned"},
		},
		{
			name: "two workspaces labelled with the repo's name",
			repo: "o/dup",
			errs: []string{`2 herdr workspaces labelled "dup"`, `projects."o/dup".workspace`},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bin := t.TempDir()
			exe := filepath.Join(bin, "herdr")
			writeHerdr(t, exe)
			write(t, filepath.Join(bin, "workspaces"), spaces)
			if c.panes != "" {
				write(t, filepath.Join(bin, "panes"), c.panes)
			}
			projects := config.Projects{Root: "/src", Repos: map[string]config.Project{c.repo: c.override}}
			got, _, err := send.ProjectWorkspace(context.Background(), herdr.New(exe, nil), projects, c.repo)
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
			if c.calls != "" {
				if b, _ := os.ReadFile(filepath.Join(bin, "calls")); string(b) != c.calls {
					t.Errorf("herdr calls %q, want %q", b, c.calls)
				}
			}
		})
	}
}

// TestFreshByConvention dispatches a ref of a project with no entry: its checkout is under
// projects.root, and its herdr workspace the one with a pane there.
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
	write(t, filepath.Join(f.bin, "panes"), `{"result":{"panes":[{"workspace_id":"w2","cwd":"`+filepath.Join(checkout, "sub")+`"}]}}`)
	res, err := f.d.Dispatch(context.Background(), date, ref(t, "o/r#11"), Fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.To != "iac" || !strings.Contains(f.calls(t), "[tab][create][--workspace][w2]") {
		t.Errorf("result %+v, calls:\n%s", res, f.calls(t))
	}

	// A project with no checkout under the root is refused before herdr is asked.
	if err := os.Remove(filepath.Join(f.bin, "calls")); err != nil {
		t.Fatal(err)
	}
	_, err = f.d.Dispatch(context.Background(), date, ref(t, "o/g#5"), Fresh, "")
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "o", "g")) {
		t.Fatalf("err %v, want the conventional path", err)
	}
	if f.calls(t) != "" {
		t.Errorf("herdr was asked: %s", f.calls(t))
	}
}
