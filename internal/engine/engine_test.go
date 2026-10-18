package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

const date = "2026-10-18"

const fixture = `# Worklog — 2026-10-18

**08:00 UTC:** testing.

## Next

1. [ ] Rotate the token · [[#^rotate|details]]

## ⬜ o/r

### ⬜ What remains

#### ⬜ Your actions

- [ ] Rotate the token ^rotate
- [ ] Renew the cert ^renew

#### ⬜ Agent queue

- ⬜ Build it ^build
`

// holdLock makes the test binary a lock holder: it takes the lock at the path, prints
// "locked" and waits to be killed.
var holdLock = flag.String("hold-lock", "", "take the lock at this path and wait to be killed")

func TestMain(m *testing.M) {
	flag.Parse()
	if *holdLock != "" {
		unlock, err := lock(context.Background(), *holdLock, time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer unlock()
		fmt.Println("locked")
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

// vault is a test vault: a repo in a temp dir, the vault in its worklog/ dir, and the note of
// date holding src.
type vault struct {
	*Engine
	path string // the note's path
}

// newVault makes a vault holding src, as a jj repo with src committed when jj is set.
func newVault(t *testing.T, src string, jj bool) vault {
	t.Helper()
	repo := t.TempDir()
	e := &Engine{Repo: repo, Dir: "worklog", LockTimeout: 5 * time.Second}
	v := vault{Engine: e, path: filepath.Join(repo, "worklog", date+".md")}
	if err := os.MkdirAll(filepath.Dir(v.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if !jj {
		return v
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
	e.JJ, e.Env = bin, []string{"HOME=" + home, "JJ_CONFIG=" + cfg}
	initRepo := exec.Command(bin, "git", "init", repo)
	initRepo.Env = e.Env
	if out, err := initRepo.CombinedOutput(); err != nil {
		t.Fatalf("jj git init: %v\n%s", err, out)
	}
	v.jj(t, "commit", "-m", "init")
	return v
}

// jj runs jj in the vault's repo and returns its stdout.
func (v vault) jj(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(v.JJ, append([]string{"-R", v.Repo}, args...)...)
	cmd.Env, cmd.Dir = v.Env, v.Repo // jj prints paths relative to its cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("jj %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}

// descriptions returns the first lines of the descriptions of @'s ancestors, newest first,
// without the root.
func (v vault) descriptions(t *testing.T) []string {
	t.Helper()
	out := v.jj(t, "log", "--no-graph", "-r", "::@- ~ root()", "-T", `description.first_line() ++ "\n"`)
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

func (v vault) read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// noStrays fails when the vault dir holds anything but the note and the lock directory.
func (v vault) noStrays(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(v.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != date+".md" && e.Name() != ".quartermaster" {
			t.Errorf("stray file in the vault: %s", e.Name())
		}
	}
}

// tick is an edit that ticks the manual action with id.
func tick(id string) Edit {
	return func(n *note.Note, src []byte) ([]byte, string, error) {
		i := slices.IndexFunc(n.Actions, func(a note.ManualAction) bool { return a.ID == id })
		if i < 0 {
			return nil, "", fmt.Errorf("no action %s", id)
		}
		out := bytes.Clone(src)
		out[n.Actions[i].Mark] = 'x'
		return out, "tick " + id, nil
	}
}

// replace is an edit that replaces old with new in the note's source, once.
func replace(old, new string) Edit {
	return func(_ *note.Note, src []byte) ([]byte, string, error) {
		if !bytes.Contains(src, []byte(old)) {
			return nil, "", fmt.Errorf("no %q in the note", old)
		}
		return bytes.Replace(src, []byte(old), []byte(new), 1), "replace", nil
	}
}

func TestMutateCommitsTheNoteAlone(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, true)
	res, err := v.Mutate(t.Context(), date, "item_transition", tick("renew"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || !res.Changed || res.Path != v.path || res.Note == nil {
		t.Errorf("result %+v", res)
	}
	got := v.read(t)
	if !strings.Contains(got, "- [x] Renew the cert ^renew\n") {
		t.Errorf("not ticked:\n%s", got)
	}
	if string(res.Src) != got {
		t.Errorf("Src is not what was written:\n%s", res.Src)
	}
	// One open, one done: the headings over them are 🔄 (spec §4.2); the agent queue stays ⬜.
	for _, h := range []string{"## 🔄 o/r\n", "### 🔄 What remains\n", "#### 🔄 Your actions\n", "#### ⬜ Agent queue\n"} {
		if !strings.Contains(got, h) {
			t.Errorf("no %q in:\n%s", h, got)
		}
	}
	want := []string{
		"worklog(2026-10-18): item_transition: tick renew",
		"chore(vault): ignore .quartermaster/",
		"init",
	}
	if d := v.descriptions(t); !slices.Equal(d, want) {
		t.Errorf("history %q, want %q", d, want)
	}
	if files := v.jj(t, "diff", "-r", "@-", "--name-only"); files != "worklog/2026-10-18.md\n" {
		t.Errorf("the note's change holds %q", files)
	}
	if files := v.jj(t, "diff", "-r", "@--", "--name-only"); files != ".gitignore\n" {
		t.Errorf("the .gitignore change holds %q", files)
	}
	if files := v.jj(t, "diff", "-r", "@", "--name-only"); files != "" {
		t.Errorf("the working copy holds %q: the lock is not ignored", files)
	}

	// A second write finds .quartermaster/ ignored already.
	if _, err := v.Mutate(t.Context(), date, "item_transition", tick("rotate")); err != nil {
		t.Fatal(err)
	}
	gi, err := os.ReadFile(filepath.Join(v.Repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gi) != ".quartermaster/\n" {
		t.Errorf(".gitignore is %q", gi)
	}
	if d := v.descriptions(t); len(d) != 4 || d[0] != "worklog(2026-10-18): item_transition: tick rotate" {
		t.Errorf("history %q", d)
	}
	v.noStrays(t)
}

func TestMutateKeepsAnExistingGitignore(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	gi := filepath.Join(v.Repo, ".gitignore")
	if err := os.WriteFile(gi, []byte("*.tmp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Mutate(t.Context(), date, "t", tick("renew")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Mutate(t.Context(), date, "t", tick("rotate")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(gi); string(b) != "*.tmp\n.quartermaster/\n" {
		t.Errorf(".gitignore is %q", b)
	}
}

func TestIgnores(t *testing.T) {
	t.Parallel()
	for gi, want := range map[string]bool{
		".quartermaster/":           true,
		"x\n  .quartermaster  \r\n": true,
		"**/.quartermaster/":        true,
		"/worklog/.quartermaster/":  true,
		"/worklog/.quartermaster":   true,
		"/other/.quartermaster/":    false,
		"/.quartermaster/":          false,
		"# .quartermaster/":         false,
		".quartermaster/worklog/":   false,
		"":                          false,
	} {
		if got := ignores([]byte(gi), "worklog"); got != want {
			t.Errorf("ignores(%q) = %v, want %v", gi, got, want)
		}
	}
	if !ignores([]byte("/.quartermaster/\n"), "") {
		t.Error("a root-anchored entry does not ignore a vault at the repo root")
	}
}

func TestMutateOutsideJJWritesWithoutCommit(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	res, err := v.Mutate(t.Context(), date, "t", tick("renew"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed || res.Skipped == "" || !res.Changed {
		t.Errorf("result %+v", res)
	}
	if !strings.Contains(v.read(t), "- [x] Renew") {
		t.Error("not written")
	}
	v.noStrays(t)
}

func TestMutateUnchangedWritesNothing(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, true)
	res, err := v.Mutate(t.Context(), date, "t", func(_ *note.Note, src []byte) ([]byte, string, error) {
		return src, "nothing", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Committed {
		t.Errorf("result %+v", res)
	}
	if d := v.descriptions(t); d[0] == "worklog(2026-10-18): t: nothing" {
		t.Errorf("history %q", d)
	}
}

func TestMutateCreatesAMissingNote(t *testing.T) {
	t.Parallel()
	v := newVault(t, "", false)
	if err := os.Remove(v.path); err != nil {
		t.Fatal(err)
	}
	res, err := v.Mutate(t.Context(), date, "day_open", func(n *note.Note, src []byte) ([]byte, string, error) {
		if n != nil || src != nil {
			t.Errorf("a missing note reads as %v, %q", n, src)
		}
		return []byte(fixture), "open", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := v.read(t); got != fixture || !res.Changed {
		t.Errorf("wrote %q, result %+v", got, res)
	}
}

// TestMutateEmptyNote reads an empty or whitespace-only note as a missing one, and writes over
// it: there is nothing in it to splice.
func TestMutateEmptyNote(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{"empty": "", "whitespace": "\n \t\r\n"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := newVault(t, src, false)
			res, err := v.Mutate(t.Context(), date, "day_open", func(n *note.Note, src []byte) ([]byte, string, error) {
				if n != nil || src != nil {
					t.Errorf("an empty note reads as %v, %q", n, src)
				}
				return []byte(fixture), "open", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := v.read(t); got != fixture || !res.Changed {
				t.Errorf("wrote %q, result %+v", got, res)
			}
		})
	}
}

// TestMutateReconcilesNextPairs: ticking a *Next* line ticks the action it points at (spec §4.4).
func TestMutateReconcilesNextPairs(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	if _, err := v.Mutate(t.Context(), date, "t", replace("1. [ ] Rotate", "1. [x] Rotate")); err != nil {
		t.Fatal(err)
	}
	if got := v.read(t); !strings.Contains(got, "- [x] Rotate the token ^rotate\n") {
		t.Errorf("the pair is not ticked:\n%s", got)
	}
}

// TestMutateRefusesUntick: no write turns `[x]` into `[ ]`, whatever the edit (spec §4.4).
func TestMutateRefusesUntick(t *testing.T) {
	t.Parallel()
	src := strings.Replace(fixture, "- [ ] Renew", "- [x] Renew", 1)
	src = strings.Replace(src, "- ⬜ Build it ^build", "- [x] Plain box", 1)
	// A *Next* checkbox pointing at a work item, not at a manual action: nothing re-ticks it.
	nextDone := strings.Replace(fixture, "1. [ ] Rotate the token · [[#^rotate|details]]",
		"1. [x] Ship it · [[#^build|details]]", 1)
	two := strings.Replace(fixture, "- ⬜ Build it ^build\n", "- [x] Plain box\n- [ ] Open box\n", 1)
	for name, c := range map[string]struct {
		src  string
		edit Edit
	}{
		"with an id":           {src, replace("- [x] Renew", "- [ ] Renew")},
		"without one":          {src, replace("- [x] Plain box", "- [ ] Plain box")},
		"and reworded":         {src, replace("- [x] Renew the cert", "- [ ] Renew the certificate")},
		"reworded without id":  {src, replace("- [x] Plain box", "- [ ] Plainer box")},
		"replaced without id":  {src, replace("- [x] Plain box\n", "- [ ] Another box\n")},
		"a Next line reworded": {nextDone, replace("1. [x] Ship it", "1. [ ] Ship it now")},
		// Without an id, these read as a box reworded and unticked: they take two writes.
		"reworded as another is ticked": {two,
			replace("- [x] Plain box\n- [ ] Open box\n", "- [ ] Plainer box\n- [x] Open box\n")},
		"reworded as an open one goes": {two,
			replace("- [x] Plain box\n- [ ] Open box\n", "- [ ] Plainer box\n")},
		"reworded and moved": {two, func(n *note.Note, src []byte) ([]byte, string, error) {
			out, _, err := replace("- [x] Plain box\n", "")(n, src)
			if err != nil {
				return nil, "", err
			}
			return replace("^renew\n", "^renew\n- [ ] Plainer box\n")(n, out)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := newVault(t, c.src, false)
			_, err := v.Mutate(t.Context(), date, "t", c.edit)
			if !errors.Is(err, ErrUntick) {
				t.Errorf("got %v, want ErrUntick", err)
			}
			if v.read(t) != c.src {
				t.Error("the note was written")
			}
			v.noStrays(t)
		})
	}
}

// TestMutateAllowsDroppingATickedBox: a write may remove a ticked box with its line (spec §4.4),
// as next_set does when it replaces a done *Next* line with a new open one.
func TestMutateAllowsDroppingATickedBox(t *testing.T) {
	t.Parallel()
	done := strings.Replace(fixture, "[ ] Rotate", "[x] Rotate", 2)
	plain := strings.Replace(fixture, "- ⬜ Build it ^build\n", "- [x] Plain box\n- [ ] Open box\n", 1)
	for name, c := range map[string]struct {
		src, old, new string
	}{
		"next_set": {done, "1. [x] Rotate the token · [[#^rotate|details]]",
			"1. [ ] Renew the cert · [[#^renew|details]]"},
		"without an id":        {plain, "- [x] Plain box\n", ""},
		"for a new ticked one": {plain, "- [x] Plain box\n", "- [x] Other done box\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v := newVault(t, c.src, false)
			if _, err := v.Mutate(t.Context(), date, "t", replace(c.old, c.new)); err != nil {
				t.Fatal(err)
			}
			if got := v.read(t); strings.Contains(got, c.old) || !strings.Contains(got, c.new) {
				t.Errorf("wrote\n%s", got)
			}
		})
	}
}

func TestMutateRefusesAnUnparseableNote(t *testing.T) {
	t.Parallel()
	broken := fixture + "\n- [ ] Again ^renew\n" // a duplicate id
	v := newVault(t, broken, true)
	called := false
	_, err := v.Mutate(t.Context(), date, "t", func(*note.Note, []byte) ([]byte, string, error) {
		called = true
		return []byte(fixture), "x", nil
	})
	if !errors.Is(err, ErrUnparseable) || !strings.Contains(err.Error(), "line ") {
		t.Errorf("got %v, want ErrUnparseable naming the line", err)
	}
	if called {
		t.Error("the edit ran on an unparseable note")
	}
	if v.read(t) != broken {
		t.Error("the note was written")
	}
	if d := v.descriptions(t); d[0] != "init" && d[0] != "chore(vault): ignore .quartermaster/" {
		t.Errorf("history %q", d)
	}
	v.noStrays(t)
}

func TestMutateRefusesAnUnparseableResult(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	_, err := v.Mutate(t.Context(), date, "t", replace("# Worklog — 2026-10-18\n", ""))
	if !errors.Is(err, ErrUnparseable) {
		t.Errorf("got %v, want ErrUnparseable", err)
	}
	if v.read(t) != fixture {
		t.Error("the note was written")
	}
}

func TestMutateValidatesItsInputs(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	for name, c := range map[string]struct {
		date, tool string
		edit       Edit
	}{
		"bad date":          {"2026-13-01", "t", tick("renew")},
		"traversal":         {"../x", "t", tick("renew")},
		"no tool":           {date, "", tick("renew")},
		"empty headline":    {date, "t", func(_ *note.Note, s []byte) ([]byte, string, error) { return s, " ", nil }},
		"two-line headline": {date, "t", func(_ *note.Note, s []byte) ([]byte, string, error) { return s, "a\nb", nil }},
	} {
		if _, err := v.Mutate(t.Context(), c.date, c.tool, c.edit); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if v.read(t) != fixture {
		t.Error("the note was written")
	}
	rel := &Engine{Repo: "relative", Dir: "worklog"}
	if _, err := rel.Mutate(t.Context(), date, "t", tick("renew")); err == nil {
		t.Error("a relative repo was accepted")
	}
}

// TestMutateRetriesAfterObsidianWrite: a write between read and rename is kept, and the edit
// runs again on top of it (spec §7 step 5).
func TestMutateRetriesAfterObsidianWrite(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, true)
	obsidian := strings.Replace(fixture, "testing.", "edited in Obsidian.", 1)
	v.beforeCheck = func(attempt int) {
		if attempt == 0 {
			if err := os.WriteFile(v.path, []byte(obsidian), 0o644); err != nil {
				t.Error(err)
			}
		}
	}
	calls := 0
	edit := tick("renew")
	res, err := v.Mutate(t.Context(), date, "t", func(n *note.Note, src []byte) ([]byte, string, error) {
		calls++
		return edit(n, src)
	})
	if err != nil {
		t.Fatal(err)
	}
	got := v.read(t)
	if calls != 2 || !strings.Contains(got, "edited in Obsidian.") || !strings.Contains(got, "- [x] Renew") {
		t.Errorf("%d calls, note:\n%s", calls, got)
	}
	if !res.Committed {
		t.Errorf("result %+v", res)
	}
	v.noStrays(t)
}

func TestMutateGivesUpOnANoteThatKeepsChanging(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	v.beforeCheck = func(attempt int) {
		s := strings.Replace(fixture, "testing.", fmt.Sprintf("edit %d.", attempt), 1)
		if err := os.WriteFile(v.path, []byte(s), 0o644); err != nil {
			t.Error(err)
		}
	}
	calls := 0
	edit := tick("renew")
	_, err := v.Mutate(t.Context(), date, "t", func(n *note.Note, src []byte) ([]byte, string, error) {
		calls++
		return edit(n, src)
	})
	if !errors.Is(err, ErrConcurrentEdit) {
		t.Errorf("got %v, want ErrConcurrentEdit", err)
	}
	if calls != 1+maxRetries {
		t.Errorf("%d attempts, want %d", calls, 1+maxRetries)
	}
	if got := v.read(t); strings.Contains(got, "[x]") {
		t.Errorf("the mutation was written:\n%s", got)
	}
	v.noStrays(t)
}

// TestMutatorsSerialise: a second mutator waits for the first's lock, and both edits land.
func TestMutatorsSerialise(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, true)
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var events []string
	record := func(s string) { mu.Lock(); events = append(events, s); mu.Unlock() }

	first := make(chan error)
	go func() {
		edit := tick("renew")
		_, err := v.Mutate(context.Background(), date, "first", func(n *note.Note, src []byte) ([]byte, string, error) {
			record("first in")
			close(entered)
			<-release
			record("first out")
			return edit(n, src)
		})
		first <- err
	}()
	<-entered

	// While the first holds the lock, a mutator with a short timeout gives up.
	impatient := *v.Engine
	impatient.LockTimeout = 50 * time.Millisecond
	if _, err := impatient.Mutate(t.Context(), date, "impatient", tick("rotate")); !errors.Is(err, ErrLockTimeout) {
		t.Errorf("got %v, want ErrLockTimeout", err)
	}

	second := make(chan error)
	go func() {
		edit := tick("rotate")
		_, err := v.Mutate(context.Background(), date, "second", func(n *note.Note, src []byte) ([]byte, string, error) {
			record("second in")
			return edit(n, src)
		})
		second <- err
	}()
	time.Sleep(100 * time.Millisecond) // give the second the chance to run, were it not locked out
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if want := []string{"first in", "first out", "second in"}; !slices.Equal(events, want) {
		t.Errorf("events %q, want %q", events, want)
	}
	got := v.read(t)
	if !strings.Contains(got, "- [x] Renew") || !strings.Contains(got, "- [x] Rotate the token ^rotate") {
		t.Errorf("an edit was lost:\n%s", got)
	}
	if d := v.descriptions(t); len(d) < 2 || d[0] != "worklog(2026-10-18): second: tick rotate" ||
		d[1] != "worklog(2026-10-18): first: tick renew" {
		t.Errorf("history %q", d)
	}
}

// TestLockReleasedWhenHolderKilled: the OS frees a dead holder's lock (spec §11).
func TestLockReleasedWhenHolderKilled(t *testing.T) {
	t.Parallel()
	v := newVault(t, fixture, false)
	path := filepath.Join(v.Repo, "worklog", ".quartermaster", "worklog", "locks", date+".lock")
	holder := exec.Command(os.Args[0], "-hold-lock", path)
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	holder.Stderr = os.Stderr
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("holder said %q, %v", line, err)
	}

	impatient := *v.Engine
	impatient.LockTimeout = 50 * time.Millisecond
	if _, err := impatient.Mutate(t.Context(), date, "t", tick("renew")); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("got %v while the holder lives, want ErrLockTimeout", err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	if _, err := impatient.Mutate(t.Context(), date, "t", tick("renew")); err != nil {
		t.Fatalf("after the holder died: %v", err)
	}
}
