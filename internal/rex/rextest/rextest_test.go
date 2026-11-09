package rextest

import (
	"slices"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/rex"
)

// TestFake checks the fake through rex's client, in process and through its executable, as the
// tests that use it call it: its answers, the calls it logs, and the ones it is made to fail.
func TestFake(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		client func(*Fake) rex.Client
	}{
		{"in process", (*Fake).Client},
		{"on disk", func(f *Fake) rex.Client { return rex.New(f.Bin, nil) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			f := New(t)
			claude := rex.Agent{Kind: "claude", Workspace: "w1", Tab: "w1/t1", Pane: "w1/p1", Cwd: "/src/web", Status: rex.Idle}
			omp := rex.Agent{Kind: "omp", Name: "web-glm", Workspace: "w1", Tab: "w1/t2", Pane: "w1/p2", Cwd: "/src/web", Status: rex.Working}
			shell := rex.Pane{ID: "w1/p3", Workspace: "w1", Tab: "w1/t3", Cwd: "/src/web"}
			f.Edit(func(s *State) {
				s.Workspace("w1", "web")
				s.Agent(claude)
				s.Agent(omp)
				s.Pane(shell)
				s.Block("w1/p2").Screen = "one\ntwo\n"
			})
			r := c.client(f)

			if got, err := r.WorkspaceList(ctx); err != nil || !slices.Equal(got, []rex.Workspace{{ID: "w1", Label: "web"}}) {
				t.Errorf("WorkspaceList = %+v, %v", got, err)
			}
			if got, err := r.AgentList(ctx); err != nil || !slices.Equal(got, []rex.Agent{claude, omp}) {
				t.Errorf("AgentList = %+v, %v", got, err)
			}
			if got, err := r.PaneList(ctx); err != nil || len(got) != 3 || got[2] != shell {
				t.Errorf("PaneList = %+v, %v", got, err)
			}
			if _, err := r.PaneGet(ctx, "w1/p9"); rex.Code(err) != rex.PaneNotFound {
				t.Errorf("PaneGet of no pane: %v", err)
			}
			if got, err := r.AgentRead(ctx, "w1/p2", 1); err != nil || string(got) != "two\n" {
				t.Errorf("AgentRead = %q, %v", got, err)
			}
			root := rex.Pane{ID: "w9/p1", Workspace: "w9", Tab: "w9/t1", Cwd: "/src/api"}
			if got, err := r.WorkspaceCreate(ctx, "/src/api", "api"); err != nil || got != root {
				t.Errorf("WorkspaceCreate = %+v, %v", got, err)
			}
			pane, err := r.TabCreate(ctx, "w1", "/src/web", "web-claude")
			if err != nil || pane != "w1/p10" {
				t.Errorf("TabCreate = %q, %v", pane, err)
			}
			if err := r.AgentStart(ctx, "web-claude", "claude", pane, []string{"--model", "x y"}); err != nil {
				t.Error(err)
			}
			if err := r.AgentPrompt(ctx, pane, "two\nlines"); err != nil {
				t.Error(err)
			}
			if got, err := r.PaneGet(ctx, pane); err != nil || got.Kind != "claude" || got.Status != rex.Idle {
				t.Errorf("PaneGet of the started agent = %+v, %v", got, err)
			}
			if err := r.TabClose(ctx, "w1/t10"); err != nil {
				t.Error(err)
			}
			if err := r.TabClose(ctx, "w1/t10"); rex.Code(err) != rex.TabNotFound {
				t.Errorf("TabClose of a closed tab: %v", err)
			}
			f.Edit(func(s *State) { s.Fail = map[string]string{"session.list": "", "prompt": "eaten"} })
			if _, err := r.AgentList(ctx); err == nil || err.Error() != "rex api call session.list: session.list failed" {
				t.Errorf("AgentList made to fail: %v", err)
			}
			if err := r.AgentPrompt(ctx, "w1/p1", "go"); err == nil || err.Error() != "rex api call block.event: eaten" {
				t.Errorf("AgentPrompt made to fail: %v", err)
			}

			want := "new api /src/api\n" +
				"window new w1 web-claude /src/web\n" +
				"label w1/p10 web-claude\n" +
				"paste w1/p10 claude --model 'x y'\n" +
				"send-key w1/p10 enter\n" +
				"paste w1/p10 two\nlines\n" +
				"send-key w1/p10 enter\n" +
				"close w1/t10\nclose w1/t10\n" +
				"paste w1/p1 go\n"
			if got := f.Writes(); got != want {
				t.Errorf("writes:\n%s\nwant:\n%s", got, want)
			}
			if calls := f.Calls(); calls[0] != "session.list" || !slices.Contains(calls, "capture w1/p2") {
				t.Errorf("calls:\n%s", strings.Join(calls, "\n"))
			}
		})
	}
}
