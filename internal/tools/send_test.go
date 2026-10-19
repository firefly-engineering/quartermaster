package tools

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

// sendDeps are the fixture's deps with herdr h, acme/web's checkout at /src/web, and the
// harnesses claude and glm (omp), glm for basic-implementation and claude for the rest.
func sendDeps(t *testing.T, src string, h fakeHerdr) Deps {
	t.Helper()
	d := fixtureDeps(t, src, true)
	d.Herdr = h.Bin
	d.Config.Projects.Repos = map[string]config.Project{"acme/web": {Path: "/src/web"}}
	d.Config.Harness = map[string]config.Harness{
		"claude": {Kind: "claude"},
		"glm":    {Kind: "omp", Args: []string{"--model", "glm-5"}},
	}
	d.Config.Tiers = map[string]string{config.DefaultTier: "claude", "basic-implementation": "glm"}
	return d
}

// mixed is a workspace of claude and omp agents in which only w1:p2 (omp) and w1:p6 (claude)
// are available: the rest are busy, in a fresh dispatch's checkout, or in a sibling directory.
func mixed() []herdr.Agent {
	return []herdr.Agent{
		herdrAgent("claude", "", "working", "/src/web", 1),
		herdrAgent("omp", "web-glm", "idle", "/src/web", 2),
		herdrAgent("claude", "web-14", "idle", "/ws/acme/web/issue-14", 3),
		herdrAgent("claude", "", "idle", "/src/webapp", 4),
		herdrAgent("claude", "", "blocked", "/src/web", 5),
		herdrAgent("claude", "", "done", "/src/web/pkg", 6),
		herdrAgent("claude", "", "idle", "/src/web", 7),
		herdrAgent("claude", "", "unknown", "/src/web", 8),
	}
}

// grill is the fixture's session-store body line, which a tier is added to.
const grill = "Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`."

// TestPromptSend sends a work item's prompt, as a verb, to the first available agent of the
// harness its tier routes to in a mixed workspace, and records the agent's pane on the item.
func TestPromptSend(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, tier, pane string
		command          string // the prompt's command, as the pane's harness runs it
	}{
		{"no tier goes to default", "", "w1:p6", "/grill-with-docs"},
		{"a tier [tiers] lists", "basic-implementation", "w1:p2", "/skill:grill-with-docs"},
		{"a tier [tiers] does not list", "design", "w1:p6", "/grill-with-docs"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := settled(t)
			if c.tier != "" {
				src = edited(t, grill, grill+" Tier: `"+c.tier+"`.")
			}
			h := newFakeHerdr(t, mixed()...)
			d := sendDeps(t, src, h)
			path := filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir, fixtureDate+".md")

			var out bytes.Buffer
			if err := RunVerb(t.Context(), All(d), "send", []byte(`{"item":"session-store"}`), &out); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{`"item": "session-store"`, `"to": "web"`, `"pane": "` + c.pane + `"`,
				`"path": "` + path + `"`, `"changed": true`, `"committed": true`} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %s:\n%s", want, out.String())
				}
			}
			if want := "[workspace][list]\n[agent][list]\n[agent][prompt][" + c.pane + "][" + c.command + " https://github.com/acme/web/issues/14\n\n" +
				send.Closing(send.Item("session-store"), path) + "]\n"; h.calls(t) != want {
				t.Errorf("herdr got\n%s\nwant\n%s", h.calls(t), want)
			}
			const button = "_item=session-store) ^session-store"
			d.checkNote(t, strings.Replace(src, button, "_item=session-store) <!-- qm pane="+c.pane+" --> ^session-store", 1))
		})
	}
}

// TestPromptSendKeepsFreshMarker leaves a fresh dispatch's marker as it is: its worker stays
// the item's agent, so refresh still closes its tab and removes its checkout (#146).
func TestPromptSendKeepsFreshMarker(t *testing.T) {
	t.Parallel()
	const button = "_item=session-store) ^session-store"
	src := edited(t, button, "_item=session-store) <!-- qm pane=w1:p3 ws=/ws/acme/web/issue-14 --> ^session-store")
	h := newFakeHerdr(t, mixed()...)
	d := sendDeps(t, src, h)
	out, err := d.promptSend(t.Context(), PromptSendInput{Item: "session-store"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pane != "w1:p6" || out.Changed {
		t.Errorf("sent to %q, note changed %v; want w1:p6 and the note unchanged", out.Pane, out.Changed)
	}
	if calls := h.calls(t); !strings.Contains(calls, "[agent][prompt]") {
		t.Errorf("herdr was not prompted:\n%s", calls)
	}
	d.checkNote(t, src)
}

// TestPromptSendSpawns starts an agent of the harness the item's tier routes to when none is
// available, in a new tab of the workspace at the checkout, without focus, then prompts it and
// records its pane. It is named the first free <repo>-<harness> among all live agents, in any
// workspace, and busy agents of the kind never prevent it.
func TestPromptSendSpawns(t *testing.T) {
	t.Parallel()
	busy := []herdr.Agent{
		herdrAgent("claude", "", "working", "/src/web", 1),
		herdrAgent("claude", "", "blocked", "/src/web", 2),
		herdrAgent("claude", "", "unknown", "/src/web", 3),
		herdrAgent("claude", "web-14", "idle", "/ws/acme/web/issue-14", 4),
		herdrAgent("omp", "", "idle", "/src/web", 5),
	}
	elsewhere := herdrAgent("claude", "web-claude-2", "idle", "/src/other/web", 2)
	elsewhere.Workspace, elsewhere.Pane = "w2", "w2:p2"
	for _, c := range []struct {
		name, tier string
		agents     []herdr.Agent
		start      string // agent start's arguments after its name
		agent      string // the name it starts
		command    string // the prompt's command, as the harness runs it
	}{
		{"no agent", "", nil, "[--kind][claude][--pane][w1:p9]", "web-claude", "/grill-with-docs"},
		{"only busy agents", "", busy, "[--kind][claude][--pane][w1:p9]", "web-claude", "/grill-with-docs"},
		{"only busy agents of a harness with args", "basic-implementation",
			[]herdr.Agent{herdrAgent("omp", "web-glm", "working", "/src/web", 1), herdrAgent("claude", "", "idle", "/src/web", 2)},
			"[--kind][omp][--pane][w1:p9][--][--model][glm-5]", "web-glm-2", "/skill:grill-with-docs"},
		{"a name collision", "", []herdr.Agent{herdrAgent("claude", "web-claude", "working", "/src/web", 1), elsewhere},
			"[--kind][claude][--pane][w1:p9]", "web-claude-3", "/grill-with-docs"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := settled(t)
			if c.tier != "" {
				src = edited(t, grill, grill+" Tier: `"+c.tier+"`.")
			}
			h := newFakeHerdr(t, c.agents...)
			d := sendDeps(t, src, h)
			path := filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir, fixtureDate+".md")
			out, err := d.promptSend(t.Context(), PromptSendInput{Item: "session-store"})
			if err != nil {
				t.Fatal(err)
			}
			if out.Pane != "w1:p9" || out.To != "web" || !out.Committed {
				t.Errorf("result %+v", out)
			}
			want := "[workspace][list]\n[agent][list]\n" +
				"[tab][create][--workspace][w1][--cwd][/src/web][--label][" + c.agent + "][--no-focus]\n" +
				"[agent][start][" + c.agent + "]" + c.start + "\n" +
				"[agent][prompt][w1:p9][" + c.command + " https://github.com/acme/web/issues/14\n\n" +
				send.Closing(send.Item("session-store"), path) + "]\n"
			if got := h.calls(t); got != want {
				t.Errorf("herdr got\n%s\nwant\n%s", got, want)
			}
			const button = "_item=session-store) ^session-store"
			d.checkNote(t, strings.Replace(src, button, "_item=session-store) <!-- qm pane=w1:p9 --> ^session-store", 1))
		})
	}
}

// TestPromptSendRefuses checks prompt_send keeps send's validation, and prompts no agent and
// leaves the note as it was when it refuses.
func TestPromptSendRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		agents []herdr.Agent
		fail   string // the herdr call made to fail
		in     PromptSendInput
		want   string
	}{
		{"no herdr", nil, "", PromptSendInput{Item: "session-store"}, "herdr not found"},
		{"a bad date", mixed(), "", PromptSendInput{Date: "today", Item: "session-store"}, "not a date"},
		{"a bad id", mixed(), "", PromptSendInput{Item: "../x"}, "not an item id"},
		{"no note", mixed(), "", PromptSendInput{Date: "2026-10-15", Item: "session-store"}, "no note"},
		{"no prompt", mixed(), "", PromptSendInput{Item: "nope"}, "no herdr-prompt block"},
		{"a spawn's tab not created", []herdr.Agent{}, "tab create", PromptSendInput{Item: "session-store"}, "tab create failed"},
		{"a spawn's agent not started", []herdr.Agent{}, "agent start", PromptSendInput{Item: "session-store"},
			"agent start failed; its herdr tab, pane w1:p9, is left in place"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newFakeHerdr(t, c.agents...)
			d := sendDeps(t, settled(t), h)
			if c.agents == nil {
				d.Herdr = ""
			}
			if c.fail != "" {
				h.fail(t, c.fail, "")
			}
			if _, err := d.promptSend(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%v, want %q", err, c.want)
			}
			if calls := h.calls(t); strings.Contains(calls, "[agent][prompt]") {
				t.Errorf("herdr was prompted:\n%s", calls)
			}
			d.checkNote(t, settled(t))
		})
	}
}

// TestTaskDispatchSession dispatches a ref in session mode through the send path prompt_send
// takes: the harness of the item's tier, and its pane recorded.
func TestTaskDispatchSession(t *testing.T) {
	t.Parallel()
	queue := "- Wave 2 after the stack lands.\n"
	item := "- ⬜ [#20](https://github.com/acme/web/issues/20): rename the store · Tier: `basic-implementation` ^rename\n"
	h := newFakeHerdr(t, mixed()...)
	d := sendDeps(t, edited(t, queue, item+queue), h)
	res, err := d.taskDispatch(t.Context(), TaskDispatchInput{Ref: "acme/web#20"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pane != "w1:p2" || res.Item != "rename" || !res.Committed {
		t.Errorf("result %+v", res)
	}
	d.checkNote(t, edited(t, queue, strings.Replace(item, " ^rename", " <!-- qm pane=w1:p2 --> ^rename", 1)+queue))
}

// toRepo is the fixture's prompt block naming its project, acme/web, rather than a workspace.
var toRepo = []string{"id=session-store to=web", "id=session-store to=acme/web"}

// TestPromptSendToRepo resolves a block's to=owner/repo through the project's herdr workspace
// (spec §9.2): one found is sent to as a label's is, and with none, one is created at the
// checkout, labelled with the override or the repo's name, without focus, and the spawn takes
// its initial shell pane rather than a new tab.
func TestPromptSendToRepo(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		override string // the project's workspace override
		spaces   []herdr.Workspace
		agents   []herdr.Agent
		create   string // the label workspace create is given; "" for none
		to, pane string
	}{
		{name: "a workspace labelled with the repo's name", spaces: []herdr.Workspace{{ID: "w1", Label: "web"}},
			agents: mixed(), to: "web", pane: "w1:p6"},
		{name: "none: created with the repo's name", create: "web", to: "web", pane: "w9:p1"},
		{name: "none: created with the override", override: "site", spaces: []herdr.Workspace{{ID: "w1", Label: "web"}},
			create: "site", to: "site", pane: "w9:p1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := edited(t, toRepo...)
			h := newFakeHerdr(t)
			h.workspaces(t, c.spaces...)
			d := sendDeps(t, src, h)
			checkout := t.TempDir()
			agents := c.agents
			for i := range agents {
				agents[i].Cwd = strings.Replace(agents[i].Cwd, "/src/web", checkout, 1)
			}
			h.agents(t, agents...)
			d.Config.Projects.Repos["acme/web"] = config.Project{Path: checkout, Workspace: c.override}
			path := filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir, fixtureDate+".md")

			out, err := d.promptSend(t.Context(), PromptSendInput{Item: "session-store"})
			if err != nil {
				t.Fatal(err)
			}
			if out.To != c.to || out.Pane != c.pane || !out.Committed {
				t.Errorf("result %+v", out)
			}
			calls := h.calls(t)
			prompt := "[agent][prompt][" + c.pane + "][/grill-with-docs https://github.com/acme/web/issues/14\n\n" +
				send.Closing(send.Item("session-store"), path) + "]\n"
			want := "[workspace][create][--cwd][" + checkout + "][--label][" + c.create + "][--no-focus]\n[agent][list]\n" +
				"[agent][start][web-claude][--kind][claude][--pane][w9:p1]\n" + prompt
			if c.create == "" {
				want = "[agent][list]\n" + prompt
			}
			if !strings.HasSuffix(calls, want) || strings.Contains(calls, "[tab]") {
				t.Errorf("herdr got\n%s\nwant it to end\n%s", calls, want)
			}
			const button = "_item=session-store) ^session-store"
			d.checkNote(t, strings.Replace(src, button, "_item=session-store) <!-- qm pane="+c.pane+" --> ^session-store", 1))
		})
	}
}

// TestPromptSendToRefuses checks what a block's to= never does: a bare label is looked up
// only, never created; to=owner/repo creates nothing without a checkout, clones nothing, and
// keeps panes of two workspaces in the checkout an error naming both.
func TestPromptSendToRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		to       bool // the block names acme/web, not the label web
		checkout bool // the checkout exists
		panes    []herdr.Pane
		want     []string
	}{
		{name: "a bare label with no workspace", checkout: true, want: []string{`0 herdr workspaces labelled "web"`}},
		{name: "owner/repo with no checkout", to: true, want: []string{"no checkout of it at", "nothing is cloned"}},
		{name: "owner/repo with panes of two workspaces in the checkout", to: true, checkout: true,
			panes: []herdr.Pane{{ID: "w2:p1", Workspace: "w2", Cwd: "CHECKOUT"}, {ID: "w3:p1", Workspace: "w3", Cwd: "CHECKOUT/sub"}},
			want:  []string{`"w2" (w2)`, `"w3" (w3)`, `projects."acme/web".workspace`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := settled(t)
			if c.to {
				src = edited(t, toRepo...)
			}
			h := newFakeHerdr(t)
			h.workspaces(t)
			d := sendDeps(t, src, h)
			checkout := filepath.Join(t.TempDir(), "web")
			if c.checkout {
				checkout = t.TempDir()
			}
			for i := range c.panes {
				c.panes[i].Cwd = strings.Replace(c.panes[i].Cwd, "CHECKOUT", checkout, 1)
			}
			h.panes(t, c.panes...)
			d.Config.Projects.Repos["acme/web"] = config.Project{Path: checkout}

			_, err := d.promptSend(t.Context(), PromptSendInput{Item: "session-store"})
			for _, w := range c.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("%v, want %q", err, w)
				}
			}
			if calls := h.calls(t); strings.Contains(calls, "[create]") || strings.Contains(calls, "[agent][prompt]") {
				t.Errorf("herdr created or prompted:\n%s", calls)
			}
			d.checkNote(t, src)
		})
	}
}
