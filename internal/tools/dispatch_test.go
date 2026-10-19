package tools

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/config"
)

// TestTaskDispatchRefuses checks the input task_dispatch validates itself; internal/dispatch
// tests the dispatching.
func TestTaskDispatchRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in   TaskDispatchInput
		want string
	}{
		{TaskDispatchInput{Ref: "#12"}, "owner/repo#N"},
		{TaskDispatchInput{Ref: "acme/web#12", Mode: "later"}, "mode"},
		{TaskDispatchInput{Ref: "acme/web#12", Date: "today"}, "not a date"},
	} {
		if _, err := d.taskDispatch(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.want)
		}
	}
}

// TestTaskDispatchFreshRoutesByTier dispatches fresh items of each tier: [tiers] picks the
// harness the new agent starts as, and an item with no tier, or one [tiers] does not list,
// goes to default.
func TestTaskDispatchFreshRoutesByTier(t *testing.T) {
	t.Parallel()
	queue := "- Wave 2 after the stack lands.\n"
	items := "- ⬜ [#20](https://github.com/acme/web/issues/20): rename the store · Route: `/implement` · Tier: `refactoring` ^rename\n" +
		"- ⬜ [#21](https://github.com/acme/web/issues/21): fix the form · Route: `/implement` ^form\n" +
		"- ⬜ [#22](https://github.com/acme/web/issues/22): cache headers · Tier: `research` ^cache\n"
	for _, c := range []struct {
		ref, id, start string
	}{
		{"acme/web#20", "rename", "[agent][start][web-20][--kind][omp][--pane][w1:p9][--][--model][glm-5]\n"},
		{"acme/web#21", "form", "[agent][start][web-21][--kind][claude][--pane][w1:p9]\n"},
		{"acme/web#22", "cache", "[agent][start][web-22][--kind][claude][--pane][w1:p9]\n"},
	} {
		t.Run(c.ref, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, edited(t, queue, items+queue), true)
			h := newFakeHerdr(t)
			d.Herdr = h.Bin
			checkout := filepath.Join(t.TempDir(), "web")
			d.jj(t, "git", "init", checkout)
			d.Config.Dispatch.WorkspaceRoot = filepath.Join(t.TempDir(), "ws")
			d.Config.Projects.Repos = map[string]config.Project{"acme/web": {Path: checkout}}
			d.Config.Harness = map[string]config.Harness{
				"claude": {Kind: "claude"},
				"glm":    {Kind: "omp", Args: []string{"--model", "glm-5"}},
			}
			d.Config.Tiers = map[string]string{config.DefaultTier: "claude", "refactoring": "glm"}

			var out bytes.Buffer
			in := `{"ref":"` + c.ref + `","mode":"fresh","date":"` + fixtureDate + `"}`
			if err := RunVerb(t.Context(), All(d), "dispatch", []byte(in), &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"item": "`+c.id+`"`) || !strings.Contains(out.String(), `"pane": "w1:p9"`) {
				t.Errorf("output:\n%s", out.String())
			}
			if calls := h.calls(t); !strings.Contains(calls, c.start) {
				t.Errorf("herdr calls:\n%s\nlack %s", calls, c.start)
			}
			if got := d.note(t, fixtureDate); !strings.Contains(got, "<!-- qm pane=w1:p9 ws=") {
				t.Errorf("no dispatch marker on ^%s:\n%s", c.id, got)
			}
		})
	}
}
