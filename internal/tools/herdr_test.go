package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
)

// herdrScript is a herdr 0.9.5 stand-in. It logs each call's arguments as `[arg]` runs, one
// call per line, in calls. A file named fail-<noun>-<verb> makes that call fail, writing the
// file's text to standard error, or "<noun> <verb> failed" when it is empty. The files
// workspaces, agents and panes are the answers of the list calls, and pane-<id> is `pane get
// <id>`'s; a pane with no file is not found. `workspace create` answers a new workspace w9
// and `tab create` a new tab t9 in its --workspace, each with one pane at its --cwd. Every
// answer is fixed: a create or a close changes no list.
const herdrScript = `#!/bin/sh
dir=$(dirname "$0")
for a in "$@"; do printf '[%s]' "$a" >> "$dir/calls"; done
echo >> "$dir/calls"
if [ -e "$dir/fail-$1-$2" ]; then
  if [ -s "$dir/fail-$1-$2" ]; then cat "$dir/fail-$1-$2" >&2; else echo "$1 $2 failed" >&2; fi
  exit 1
fi
call="$1 $2" id=$3 cwd= ws=
shift 2
while [ $# -gt 0 ]; do
  case "$1" in --cwd) cwd=$2; shift ;; --workspace) ws=$2; shift ;; esac
  shift
done
case "$call" in
  "workspace list") cat "$dir/workspaces" ;;
  "agent list") cat "$dir/agents" ;;
  "pane list") cat "$dir/panes" ;;
  "pane get") if [ -e "$dir/pane-$id" ]; then cat "$dir/pane-$id"; else
    echo '{"error":{"code":"pane_not_found","message":"pane '"$id"' not found"},"id":"cli:pane:get"}' >&2; exit 1; fi ;;
  "workspace create") printf '{"result":{"root_pane":{"pane_id":"w9:p1","tab_id":"w9:t1","workspace_id":"w9","cwd":"%s","agent_status":"unknown"}}}\n' "$cwd" ;;
  "tab create") printf '{"result":{"tab":{"tab_id":"%s:t9"},"root_pane":{"pane_id":"%s:p9","tab_id":"%s:t9","workspace_id":"%s","cwd":"%s","agent_status":"unknown"}}}\n' "$ws" "$ws" "$ws" "$ws" "$cwd" ;;
  "tab close") echo '{"result":{"type":"ok"}}' ;;
  "agent start"|"agent prompt") ;;
  *) echo "unexpected: $call" >&2; exit 3 ;;
esac
`

// fakeHerdr is a herdrScript in its own directory.
type fakeHerdr struct {
	Bin string // its path, for Deps.Herdr
	dir string
}

// newFakeHerdr writes a herdrScript whose one workspace, w1, is labelled web, and whose
// agents are agents, each in its own pane.
func newFakeHerdr(t *testing.T, agents ...herdr.Agent) fakeHerdr {
	t.Helper()
	dir := t.TempDir()
	h := fakeHerdr{Bin: filepath.Join(dir, "herdr"), dir: dir}
	// Hold ForkLock so no parallel fork inherits the write fd: golang/go#22315 (ETXTBSY).
	syscall.ForkLock.RLock()
	err := os.WriteFile(h.Bin, []byte(herdrScript), 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	h.workspaces(t, herdr.Workspace{ID: "w1", Label: "web"})
	h.agents(t, agents...)
	return h
}

// herdrAgent is an agent of kind, named name, of status, alone in tab n of workspace w1, at cwd.
func herdrAgent(kind, name, status, cwd string, n int) herdr.Agent {
	tab, pane := "w1:t"+strconv.Itoa(n), "w1:p"+strconv.Itoa(n)
	return herdr.Agent{Kind: kind, Name: name, Workspace: "w1", Tab: tab, Pane: pane, Cwd: cwd, Status: status}
}

// workspaces sets `workspace list`'s answer.
func (h fakeHerdr) workspaces(t *testing.T, ws ...herdr.Workspace) {
	t.Helper()
	h.answer(t, "workspaces", "workspaces", ws)
}

// agents sets `agent list`'s answer, and the panes they are in as `pane list`'s and `pane
// get`'s.
func (h fakeHerdr) agents(t *testing.T, agents ...herdr.Agent) {
	t.Helper()
	h.answer(t, "agents", "agents", agents)
	panes := make([]herdr.Pane, len(agents))
	for i, a := range agents {
		panes[i] = herdr.Pane{ID: a.Pane, Workspace: a.Workspace, Tab: a.Tab, Cwd: a.Cwd, Kind: a.Kind, Status: a.Status}
	}
	h.panes(t, panes...)
}

// panes sets `pane list`'s answer and each pane's `pane get`.
func (h fakeHerdr) panes(t *testing.T, panes ...herdr.Pane) {
	t.Helper()
	h.answer(t, "panes", "panes", panes)
	for _, p := range panes {
		h.answer(t, "pane-"+p.ID, "pane", p)
	}
}

// fail makes call, as "tab close", fail with stderr, or a plain message when it is "".
func (h fakeHerdr) fail(t *testing.T, call, stderr string) {
	t.Helper()
	h.write(t, "fail-"+strings.ReplaceAll(call, " ", "-"), stderr)
}

// calls are the calls made so far, one per line.
func (h fakeHerdr) calls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.dir, "calls"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// answer writes file as herdr's answer {"result": {key: v}}.
func (h fakeHerdr) answer(t *testing.T, file, key string, v any) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"result": map[string]any{key: v}})
	if err != nil {
		t.Fatal(err)
	}
	h.write(t, file, string(b)+"\n")
}

func (h fakeHerdr) write(t *testing.T, file, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, file), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFakeHerdr checks the fake's answers decode through the herdr client, as the tools'
// own calls do, and that it logs every call and fails the ones asked to.
func TestFakeHerdr(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	claude := herdrAgent("claude", "", "idle", "/src/web", 1)
	omp := herdrAgent("omp", "web-glm", "working", "/src/web", 2)
	h := newFakeHerdr(t, claude, omp)
	c := herdr.New(h.Bin, nil)

	if got, err := c.WorkspaceList(ctx); err != nil || !slices.Equal(got, []herdr.Workspace{{ID: "w1", Label: "web"}}) {
		t.Errorf("WorkspaceList = %+v, %v", got, err)
	}
	if got, err := c.AgentList(ctx); err != nil || !slices.Equal(got, []herdr.Agent{claude, omp}) {
		t.Errorf("AgentList = %+v, %v", got, err)
	}
	pane := herdr.Pane{ID: "w1:p2", Workspace: "w1", Tab: "w1:t2", Cwd: "/src/web", Kind: "omp", Status: "working"}
	if got, err := c.PaneList(ctx); err != nil || len(got) != 2 || got[1] != pane {
		t.Errorf("PaneList = %+v, %v", got, err)
	}
	if got, err := c.PaneGet(ctx, "w1:p2"); err != nil || got != pane {
		t.Errorf("PaneGet = %+v, %v", got, err)
	}
	if _, err := c.PaneGet(ctx, "w1:p9"); herdr.Code(err) != herdr.PaneNotFound {
		t.Errorf("PaneGet of no pane: %v", err)
	}
	root := herdr.Pane{ID: "w9:p1", Workspace: "w9", Tab: "w9:t1", Cwd: "/src/api", Status: "unknown"}
	if got, err := c.WorkspaceCreate(ctx, "/src/api", "api"); err != nil || got != root {
		t.Errorf("WorkspaceCreate = %+v, %v", got, err)
	}
	if got, err := c.TabCreate(ctx, "w1", "/src/web", "web-claude"); err != nil || got != "w1:p9" {
		t.Errorf("TabCreate = %q, %v", got, err)
	}
	if err := c.AgentStart(ctx, "web-claude", "claude", "w1:p9", nil); err != nil {
		t.Error(err)
	}
	if err := c.AgentPrompt(ctx, "w1:p9", "two\nlines"); err != nil {
		t.Error(err)
	}
	if err := c.TabClose(ctx, "w1:t9"); err != nil {
		t.Error(err)
	}
	h.fail(t, "tab close", `{"error":{"code":"tab_not_found","message":"tab w1:t9 not found"}}`)
	if err := c.TabClose(ctx, "w1:t9"); herdr.Code(err) != herdr.TabNotFound {
		t.Errorf("TabClose made to fail: %v", err)
	}
	h.fail(t, "agent list", "")
	if _, err := c.AgentList(ctx); err == nil || err.Error() != "herdr agent list: agent list failed" {
		t.Errorf("AgentList made to fail: %v", err)
	}

	want := "[workspace][list]\n[agent][list]\n[pane][list]\n[pane][get][w1:p2]\n[pane][get][w1:p9]\n" +
		"[workspace][create][--cwd][/src/api][--label][api][--no-focus]\n" +
		"[tab][create][--workspace][w1][--cwd][/src/web][--label][web-claude][--no-focus]\n" +
		"[agent][start][web-claude][--kind][claude][--pane][w1:p9]\n" +
		"[agent][prompt][w1:p9][two\nlines]\n" +
		"[tab][close][w1:t9]\n[tab][close][w1:t9]\n[agent][list]\n"
	if got := h.calls(t); got != want {
		t.Errorf("calls:\n%s\nwant:\n%s", got, want)
	}
}
