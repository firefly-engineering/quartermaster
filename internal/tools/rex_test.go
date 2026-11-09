package tools

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/rex"
	"github.com/firefly-engineering/quartermaster/internal/rex/rextest"
)

// fakeRex is a fake rex as the tools tests set it: workspaces, and the panes in them. A new
// tab is t9 of its workspace, its pane p9; a new workspace w9, its pane w9/p1.
type fakeRex struct{ *rextest.Fake }

// newFakeRex is a fake rex whose one workspace, w1, is labelled web, and whose agents are agents,
// each in its own pane.
func newFakeRex(t *testing.T, agents ...rex.Agent) fakeRex {
	t.Helper()
	f := fakeRex{rextest.New(t)}
	f.workspaces(rex.Workspace{ID: "w1", Label: "web"})
	f.agents(agents...)
	return f
}

// rexAgent is an agent of kind, named name, of status, alone in tab n of workspace w1, at cwd.
// A status of "" is an agent that reports nothing.
func rexAgent(kind, name, status, cwd string, n int) rex.Agent {
	tab, pane := "w1/t"+strconv.Itoa(n), "w1/p"+strconv.Itoa(n)
	return rex.Agent{Kind: kind, Name: name, Workspace: "w1", Tab: tab, Pane: pane, Cwd: cwd, Status: status}
}

// workspaces makes ws the sessions, with no pane in them.
func (f fakeRex) workspaces(ws ...rex.Workspace) {
	f.Edit(func(s *rextest.State) {
		s.Sessions = nil
		for _, w := range ws {
			s.Workspace(w.ID, w.Label)
		}
	})
}

// agents makes agents the only panes, in their workspaces, which are added unlabelled when
// there are none.
func (f fakeRex) agents(agents ...rex.Agent) {
	f.Edit(func(s *rextest.State) {
		clearPanes(s)
		for _, a := range agents {
			s.Agent(a)
		}
	})
}

// panes makes panes the only panes, in their workspaces, which are added unlabelled when there
// are none.
func (f fakeRex) panes(panes ...rex.Pane) {
	f.Edit(func(s *rextest.State) {
		clearPanes(s)
		for _, p := range panes {
			s.Pane(p)
		}
	})
}

func clearPanes(s *rextest.State) {
	for i := range s.Sessions {
		s.Sessions[i].Blocks = nil
	}
}

// fail makes call fail with stderr, or a plain message when it is "" (rextest.State.Fail).
func (f fakeRex) fail(call, stderr string) {
	f.Edit(func(s *rextest.State) {
		if s.Fail == nil {
			s.Fail = map[string]string{}
		}
		s.Fail[call] = stderr
	})
}

// wrote reports whether a write call starting with prefix was made.
func (f fakeRex) wrote(prefix string) bool {
	return slices.ContainsFunc(strings.Split(f.Writes(), "\n"), func(c string) bool { return strings.HasPrefix(c, prefix) })
}

// prompt is the write that pastes text into pane, then the Enter that submits it.
func prompt(pane, text string) string {
	return "paste " + pane + " " + text + "\nsend-key " + pane + " enter\n"
}

// start is the writes that start an agent named name in pane by typing command.
func start(pane, name, command string) string {
	return "label " + pane + " " + name + "\n" + prompt(pane, command)
}
