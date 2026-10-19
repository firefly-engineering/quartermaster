package herdr

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// The answers below are herdr 0.9.5's, recorded and cut to one or two entries.

const agentList = `{"id":"cli:agent:list","result":{"agents":[` +
	`{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"ae617c9b"},"agent_status":"working","cwd":"/src/trunk","focused":false,"foreground_cwd":"/src/trunk","pane_id":"wZ:pJ","revision":7,"state_change_seq":4166,"tab_id":"wZ:t1","terminal_id":"term_65e0","terminal_title":"◑ Claude Code","terminal_title_stripped":"Claude Code","workspace_id":"wZ"},` +
	`{"agent":"omp","agent_status":"idle","cwd":"/ws/o/r/issue-5","name":"r-5","pane_id":"w1:p7","tab_id":"w1:t5","workspace_id":"w1"}]}}`

const workspaceList = `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
	`{"active_tab_id":"wX:t1","agent_status":"unknown","focused":false,"label":"IaC","number":1,"pane_count":1,"tab_count":1,"workspace_id":"wX"}]}}`

const paneList = `{"id":"cli:pane:list","result":{"panes":[` +
	`{"agent_status":"unknown","cwd":"/src/IaC","focused":false,"foreground_cwd":"/src/IaC","pane_id":"wX:p9","revision":63,"scroll":{"max_offset_from_bottom":4508,"offset_from_bottom":0,"viewport_rows":165},"tab_id":"wX:t1","terminal_id":"term_65cd","terminal_title":"~/s/IaC","terminal_title_stripped":"~/s/IaC","workspace_id":"wX"}]}}`

const paneGet = `{"id":"cli:pane:get","result":{"pane":{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"ae617c9b"},"agent_status":"working","cwd":"/src/trunk","focused":false,"foreground_cwd":"/src/trunk","pane_id":"wZ:pJ","revision":7,"tab_id":"wZ:t1","terminal_id":"term_65e0","workspace_id":"wZ"},"type":"pane_info"}}`

const workspaceCreate = `{"id":"cli:workspace:create","result":{"root_pane":{"agent_status":"unknown","cwd":"/private/tmp","focused":false,"foreground_cwd":"/private/tmp","pane_id":"w1T:p1","revision":0,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":165},"tab_id":"w1T:t1","terminal_id":"term_65e3","workspace_id":"w1T"},` +
	`"tab":{"agent_status":"unknown","focused":false,"label":"1","number":1,"pane_count":1,"tab_id":"w1T:t1","workspace_id":"w1T"},"type":"workspace_created",` +
	`"workspace":{"active_tab_id":"w1T:t1","agent_status":"unknown","focused":false,"label":"probe","number":9,"pane_count":1,"tab_count":1,"workspace_id":"w1T"}}}`

const tabCreate = `{"id":"cli:tab:create","result":{"tab":{"tab_id":"w1:t5"},"root_pane":{"pane_id":"w1:p7"}}}`

const paneNotFound = `{"error":{"code":"pane_not_found","message":"pane nosuch:p1 not found"},"id":"cli:pane:get"}`

const tabNotFound = `{"error":{"code":"tab_not_found","message":"tab w1T:t9 not found"},"id":"cli:tab:close"}`

// answer is a Client whose herdr answers out, and records the args it was called with.
func answer(out string, calls *[]string) Client {
	return Client{Run: func(_ context.Context, args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		return []byte(out), nil
	}}
}

func TestAgentList(t *testing.T) {
	t.Parallel()
	var calls []string
	got, err := answer(agentList, &calls).AgentList(t.Context())
	want := []Agent{
		{Kind: "claude", Workspace: "wZ", Tab: "wZ:t1", Pane: "wZ:pJ", Cwd: "/src/trunk", Status: "working"},
		{Kind: "omp", Name: "r-5", Workspace: "w1", Tab: "w1:t5", Pane: "w1:p7", Cwd: "/ws/o/r/issue-5", Status: "idle"},
	}
	if err != nil || !slices.Equal(got, want) || !slices.Equal(calls, []string{"agent list"}) {
		t.Errorf("AgentList = %+v, %v after %q; want %+v", got, err, calls, want)
	}
}

func TestWorkspaceList(t *testing.T) {
	t.Parallel()
	var calls []string
	got, err := answer(workspaceList, &calls).WorkspaceList(t.Context())
	if want := []Workspace{{ID: "wX", Label: "IaC"}}; err != nil || !slices.Equal(got, want) || !slices.Equal(calls, []string{"workspace list"}) {
		t.Errorf("WorkspaceList = %+v, %v after %q; want %+v", got, err, calls, want)
	}
}

func TestPaneList(t *testing.T) {
	t.Parallel()
	var calls []string
	got, err := answer(paneList, &calls).PaneList(t.Context())
	want := []Pane{{ID: "wX:p9", Workspace: "wX", Tab: "wX:t1", Cwd: "/src/IaC", Status: "unknown"}}
	if err != nil || !slices.Equal(got, want) || !slices.Equal(calls, []string{"pane list"}) {
		t.Errorf("PaneList = %+v, %v after %q; want %+v", got, err, calls, want)
	}
}

func TestPaneGet(t *testing.T) {
	t.Parallel()
	var calls []string
	got, err := answer(paneGet, &calls).PaneGet(t.Context(), "wZ:pJ")
	want := Pane{ID: "wZ:pJ", Workspace: "wZ", Tab: "wZ:t1", Cwd: "/src/trunk", Kind: "claude", Status: "working"}
	if err != nil || got != want || !slices.Equal(calls, []string{"pane get wZ:pJ"}) {
		t.Errorf("PaneGet = %+v, %v after %q; want %+v", got, err, calls, want)
	}
	// A pane no agent occupies has no agent, or a null one.
	got, err = answer(`{"result":{"pane":{"agent":null,"pane_id":"wZ:pJ"}}}`, &calls).PaneGet(t.Context(), "wZ:pJ")
	if err != nil || got.Kind != "" {
		t.Errorf("PaneGet with a null agent = %+v, %v", got, err)
	}
}

func TestWorkspaceCreate(t *testing.T) {
	t.Parallel()
	var calls []string
	got, err := answer(workspaceCreate, &calls).WorkspaceCreate(t.Context(), "/tmp", "probe")
	want := Pane{ID: "w1T:p1", Workspace: "w1T", Tab: "w1T:t1", Cwd: "/private/tmp", Status: "unknown"}
	if err != nil || got != want || !slices.Equal(calls, []string{"workspace create --cwd /tmp --label probe --no-focus"}) {
		t.Errorf("WorkspaceCreate = %+v, %v after %q; want %+v", got, err, calls, want)
	}
	if _, err := answer(`{"result":{"workspace":{"workspace_id":"w1T"}}}`, &calls).WorkspaceCreate(t.Context(), "/tmp", "probe"); err == nil ||
		err.Error() != "herdr workspace create: no root pane in its answer" {
		t.Errorf("WorkspaceCreate with no root pane: %v", err)
	}
}

func TestTabCreate(t *testing.T) {
	t.Parallel()
	var calls []string
	pane, err := answer(tabCreate, &calls).TabCreate(t.Context(), "w1", "/ws/o/r/issue-5", "r-5")
	want := []string{"tab create --workspace w1 --cwd /ws/o/r/issue-5 --label r-5 --no-focus"}
	if err != nil || pane != "w1:p7" || !slices.Equal(calls, want) {
		t.Errorf("TabCreate = %q, %v after %q", pane, err, calls)
	}
	if _, err := answer(`{"result":{"tab":{"tab_id":"w1:t5"}}}`, &calls).TabCreate(t.Context(), "w1", "/ws", "r-5"); err == nil ||
		err.Error() != "herdr tab create: no root pane in its answer" {
		t.Errorf("TabCreate with no root pane: %v", err)
	}
}

// TestCalls checks the calls answered by plain text or nothing are made as herdr takes them.
func TestCalls(t *testing.T) {
	t.Parallel()
	var calls []string
	c := answer("the pane's text\n", &calls)
	ctx := t.Context()
	text, err := c.AgentRead(ctx, "w1:p7", 400)
	if err != nil || string(text) != "the pane's text\n" {
		t.Errorf("AgentRead = %q, %v", text, err)
	}
	if err := c.AgentStart(ctx, "r-5", "omp", "w1:p7", []string{"--model", "glm-5"}); err != nil {
		t.Error(err)
	}
	if err := c.AgentStart(ctx, "r-5", "claude", "w1:p7", nil); err != nil {
		t.Error(err)
	}
	if err := c.AgentPrompt(ctx, "w1:p7", "/implement it"); err != nil {
		t.Error(err)
	}
	if err := c.TabClose(ctx, "w1:t5"); err != nil {
		t.Error(err)
	}
	want := []string{
		"agent read w1:p7 --source recent-unwrapped --lines 400 --format text",
		"agent start r-5 --kind omp --pane w1:p7 -- --model glm-5",
		"agent start r-5 --kind claude --pane w1:p7",
		"agent prompt w1:p7 /implement it",
		"tab close w1:t5",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if _, err := (Client{}).AgentList(ctx); err == nil || err.Error() != "herdr: not available" {
		t.Errorf("the zero Client: %v", err)
	}
	if _, err := New("", nil).AgentList(ctx); err == nil || err.Error() != "herdr not found on PATH" {
		t.Errorf("no herdr: %v", err)
	}
	var calls []string
	if _, err := answer("not json", &calls).WorkspaceList(ctx); err == nil || !strings.HasPrefix(err.Error(), "herdr workspace list: ") {
		t.Errorf("an answer not JSON: %v", err)
	}
	gone := Client{Run: func(_ context.Context, args ...string) ([]byte, error) {
		return nil, &ExitError{Args: args, Stderr: []byte(paneNotFound + "\n")}
	}}
	_, err := gone.PaneGet(ctx, "nosuch:p1")
	if Code(err) != PaneNotFound || err.Error() != "herdr pane get: "+paneNotFound {
		t.Errorf("a pane not found: %v, code %q", err, Code(err))
	}
	closed := Client{Run: func(_ context.Context, args ...string) ([]byte, error) {
		return nil, &ExitError{Args: args, Stderr: []byte(tabNotFound + "\n")}
	}}
	if err := closed.TabClose(ctx, "w1T:t9"); Code(err) != TabNotFound {
		t.Errorf("a tab not found: %v, code %q", err, Code(err))
	}
	if Code(&ExitError{Args: []string{"agent", "list"}, Stdout: []byte("oops")}) != "" {
		t.Error("an exit with no JSON error has a code")
	}
}

// TestNew runs an executable as herdr: sh, whose -c is the "verb" errors name.
func TestNew(t *testing.T) {
	t.Parallel()
	out, err := New("/bin/sh", nil).Run(t.Context(), "-c", "echo out")
	if err != nil || string(out) != "out\n" {
		t.Fatalf("out %q, err %v", out, err)
	}
	_, err = New("/bin/sh", nil).Run(t.Context(), "-c", "echo '"+paneNotFound+"' >&2; exit 1")
	if Code(err) != PaneNotFound || !strings.HasPrefix(err.Error(), "herdr -c echo ") {
		t.Errorf("err %v, code %q", err, Code(err))
	}
	_, err = New("/bin/sh", nil).Run(t.Context(), "-c", "echo ' only stdout '; exit 1")
	if err == nil || !strings.HasSuffix(err.Error(), ": only stdout") {
		t.Errorf("err %v", err)
	}
}
