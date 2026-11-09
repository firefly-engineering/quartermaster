package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	sess  = "session:s1"
	shell = "block:b1"
	agent = "block:b2"
)

// fakeRex answers rex's calls from a session sess with two blocks: shell, a bare login shell, and
// agent, a claude under it. status is what program_status answers for agent, and taken what it
// answers instead once a paste has landed, when taken is set; calls records every call but the
// read-only ones.
type fakeRex struct {
	t             *testing.T
	status, taken string
	calls         []string
	// started is false until a paste lands in agent, before which it holds only its shell.
	started, pasted bool
}

func (f *fakeRex) run(_ context.Context, args ...string) ([]byte, error) {
	if args[0] != "api" {
		f.calls = append(f.calls, strings.Join(args, " "))
		switch args[0] {
		case "capture":
			return []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\n"), nil
		case "send-key":
			return nil, nil
		}
		f.t.Fatalf("unexpected rex %q", args)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(args[3]), &in); err != nil {
		f.t.Fatal(err)
	}
	switch m := args[2]; m {
	case "session.list":
		return []byte(`{"sessions":[{"session_id":"` + sess + `","label":"web"}]}`), nil
	case "session.list_blocks":
		if in["session_id"] != sess {
			return nil, notFoundExit(args, "session_not_found")
		}
		return []byte(`{"blocks":[{"block_id":"` + shell + `","label":"shell","window_id":"window:w1"},` +
			`{"block_id":"` + agent + `","label":"web-claude","window_id":"window:w2"}]}`), nil
	case "block.method":
		bare := `{"child":{"pid":10,"name":"login"},"foreground":{"pid":11,"name":"fish","cwd":"/src/web"},"ancestors":[{"pid":10,"name":"login"}]}`
		switch {
		case in["method"] == "process" && (in["block_id"] == shell || !f.started):
			return []byte(bare), nil
		case in["method"] == "process":
			return []byte(`{"child":{"pid":20,"name":"login"},"foreground":{"pid":22,"name":"claude","cwd":"/src/web"},` +
				`"ancestors":[{"pid":21,"name":"fish"},{"pid":20,"name":"login"}]}`), nil
		case in["method"] == "program_status" && f.pasted && f.taken != "":
			return []byte(f.taken), nil
		case in["method"] == "program_status":
			return []byte(f.status), nil
		}
	case "block.event":
		f.started, f.pasted = true, true
		f.calls = append(f.calls, m+" "+in["event"].(map[string]any)["payload"].(map[string]any)["text"].(string))
		return []byte(`{}`), nil
	case "session.set_block_label", "session.close_window":
		f.calls = append(f.calls, m+" "+args[3])
		if m == "session.close_window" && in["window_id"] != "window:w2" {
			return nil, notFoundExit(args, "not_found")
		}
		return []byte(`{}`), nil
	}
	f.t.Fatalf("unexpected rex %q", args)
	return nil, nil
}

func notFoundExit(args []string, code string) error {
	return &ExitError{Args: args, Stderr: []byte(fmt.Sprintf("Error: stream response \"control-2\" failed: gone (status=404 code=%s)\n", code))}
}

func status(state string, pid int) string { return statusAt(state, pid, "2026-11-09T10:00:00Z") }

// statusAt is a status whose root record rex last updated at at.
func statusAt(state string, pid int, at string) string {
	return fmt.Sprintf(`{"records":[{"state":%q,"app":"claude-code","owner":{"pid":%d,"name":"claude"},"updated_at":%q}]}`, state, pid, at)
}

func TestAgentList(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, status, want string }{
		{"the foreground program's own report", status("working", 22), "working"},
		{"a record an exited program left", status("idle", 99), ""},
		{"no report at all", `{"records":[]}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeRex{t: t, status: tt.status, started: true}
			got, err := Client{Run: f.run}.AgentList(t.Context())
			want := []Agent{{Kind: "claude", Name: "web-claude", Workspace: sess, Tab: sess + "/window:w2", Pane: sess + "/" + agent, Cwd: "/src/web", Status: tt.want}}
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestPaneGetGone(t *testing.T) {
	t.Parallel()
	c := Client{Run: (&fakeRex{t: t}).run}
	for _, id := range []string{sess + "/block:gone", "session:gone/" + shell} {
		if _, err := c.PaneGet(t.Context(), id); Code(err) != PaneNotFound {
			t.Errorf("PaneGet(%s): %v, code %q", id, err, Code(err))
		}
	}
	p, err := c.PaneGet(t.Context(), sess+"/"+shell)
	if want := (Pane{ID: sess + "/" + shell, Workspace: sess, Tab: sess + "/window:w1", Cwd: "/src/web"}); err != nil || p != want {
		t.Fatalf("got %+v, %v; want %+v", p, err, want)
	}
}

func TestTabClose(t *testing.T) {
	t.Parallel()
	c := Client{Run: (&fakeRex{t: t}).run}
	if err := c.TabClose(t.Context(), sess+"/window:w2"); err != nil {
		t.Fatal(err)
	}
	if err := c.TabClose(t.Context(), sess+"/window:gone"); Code(err) != TabNotFound {
		t.Fatalf("%v, code %q", err, Code(err))
	}
}

func TestAgentStart(t *testing.T) {
	t.Parallel()
	f := &fakeRex{t: t, status: status("idle", 22)}
	c := Client{Run: f.run, Tick: time.Millisecond, Patience: time.Second}
	if err := c.AgentStart(t.Context(), "web-claude", "claude", sess+"/"+agent, []string{"--model", "it's"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`session.set_block_label {"block_id":"block:b2","label":"web-claude","session_id":"session:s1"}`,
		`block.event claude --model 'it'\''s'`,
		"send-key -s session:s1 -b block:b2 enter",
	}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestAgentStartNeverRuns(t *testing.T) {
	t.Parallel()
	f := &fakeRex{t: t}
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		out, err := f.run(ctx, args...)
		f.started = false // the program exits at once, leaving the shell
		return out, err
	}
	c := Client{Run: run, Tick: time.Millisecond, Patience: 20 * time.Millisecond}
	if err := c.AgentStart(t.Context(), "web-claude", "claude", sess+"/"+agent, nil); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentStartNoStatus(t *testing.T) {
	t.Parallel()
	f := &fakeRex{t: t, status: `{"records":[]}`} // claude waiting on its folder-trust question
	c := Client{Run: f.run, Tick: time.Millisecond, Patience: 20 * time.Millisecond}
	err := c.AgentStart(t.Context(), "web-claude", "claude", sess+"/"+agent, nil)
	if err == nil || !strings.Contains(err.Error(), "reported no status") {
		t.Fatalf("got %v", err)
	}
	if last := f.calls[len(f.calls)-1]; last != "send-key -s session:s1 -b block:b2 enter" {
		t.Fatalf("typed after the start's Enter: %q", last)
	}
}

// TestAgentPrompt pastes the prompt, presses Enter, and returns once the agent took it: it
// reports working or blocked, or a status rex recorded after the paste, as a turn that finished
// between two looks leaves.
func TestAgentPrompt(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, before, after string }{
		{"working at once", status("idle", 22), status("working", 22)},
		{"blocked on an approval", status("done", 22), status("blocked", 22)},
		{"a turn done between looks", status("idle", 22), statusAt("done", 22, "2026-11-09T10:00:01Z")},
		{"a first report", `{"records":[]}`, status("idle", 22)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeRex{t: t, status: tt.before, taken: tt.after, started: true}
			c := Client{Run: f.run, Tick: time.Millisecond, PromptPatience: time.Second}
			if err := c.AgentPrompt(t.Context(), sess+"/"+agent, "line one\nline two"); err != nil {
				t.Fatal(err)
			}
			if want := []string{"block.event line one\nline two", "send-key -s session:s1 -b block:b2 enter"}; !slices.Equal(f.calls, want) {
				t.Fatalf("calls %q, want %q", f.calls, want)
			}
		})
	}
}

// TestAgentPromptNotTaken fails a prompt the agent never took, as omp's setup wizard takes one,
// idle and with no new report, and quotes the last 5 lines of its screen.
func TestAgentPromptNotTaken(t *testing.T) {
	t.Parallel()
	f := &fakeRex{t: t, status: status("idle", 22), started: true}
	c := Client{Run: f.run, Tick: time.Millisecond, PromptPatience: 20 * time.Millisecond}
	err := c.AgentPrompt(t.Context(), sess+"/"+agent, "go")
	if err == nil || !strings.Contains(err.Error(), "prompt not taken by "+sess+"/"+agent) ||
		!strings.HasSuffix(err.Error(), "\nthree\nfour\nfive\nsix\nseven") || strings.Contains(err.Error(), "two") {
		t.Fatalf("got %v", err)
	}
	if last := f.calls[len(f.calls)-1]; last != "capture -s session:s1 -b block:b2 --trim --unwrap" {
		t.Fatalf("last call %q, want the screen's capture", last)
	}
}

func TestAgentRead(t *testing.T) {
	t.Parallel()
	c := Client{Run: (&fakeRex{t: t}).run}
	if out, err := c.AgentRead(t.Context(), sess+"/"+agent, 2); err != nil || string(out) != "six\nseven\n" {
		t.Fatalf("read %q, %v", out, err)
	}
}

func TestBadID(t *testing.T) {
	t.Parallel()
	if _, err := (Client{Run: (&fakeRex{t: t}).run}).PaneGet(t.Context(), "w1:p1"); err == nil {
		t.Fatal("a pane id with no session read")
	}
}

// TestExitErrorVerb names a failed call by its method or command, never by a flag.
func TestExitErrorVerb(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"api", "call", "session.list", "{}"}, "rex api call session.list: gone"},
		{[]string{"capture", "-s", sess, "-b", agent, "--trim", "--unwrap"}, "rex capture: gone"},
		{[]string{"send-key", "-s", sess, "-b", agent, "enter"}, "rex send-key: gone"},
		{[]string{"new", "web", "-c", "/src/web", "--json"}, "rex new: gone"},
		{[]string{"window", "new", "-s", sess, "web", "-c", "/src/web"}, "rex window new: gone"},
	} {
		if got := (&ExitError{Args: c.args, Stderr: []byte("gone\n")}).Error(); got != c.want {
			t.Errorf("%q: %q, want %q", c.args, got, c.want)
		}
	}
}
