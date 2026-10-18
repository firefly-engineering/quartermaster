package tools

import (
	"os"
	"slices"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/2026-10-16.md")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCarryOverFullDay collects every open item of a full day's note, from every section.
func TestCarryOverFullDay(t *testing.T) {
	t.Parallel()
	src := readFixture(t)
	prev, err := note.Parse(src)
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	got, repos, err := carryOver(prev, "2026-10-16", note.Renderer{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme/web", "acme/infra"}; !slices.Equal(repos, want) {
		t.Errorf("repos %v, want %v", repos, want)
	}
	want := []note.CarryOverUnit{
		// Objectives: not the done or the missed one.
		{Item: "Ship the login page (acme/web)", State: "🔍 stack in review"},
		{Item: "Upgrade the runner (acme/infra)", State: "⏳ waits on the vendor"},
		// Next: a manual action that links nothing; the one paired with ^rotate-token is not
		// collected twice.
		{Item: "Call the vendor · 10 min", State: "TODO, manual action"},
		// A Part's manual action.
		{Item: "Check the preview deploy by hand (acme/web)", State: "TODO, manual action"},
		// What remains: a ##### item, an entry with no id, an unticked action, a list item.
		{
			Item:  "[#14](https://github.com/acme/web/issues/14): which session store? (acme/web) [[2026-10-16#^session-store|^session-store]]",
			State: "⬜ TODO in Decisions for you",
		},
		{Item: "[#16](https://github.com/acme/web/issues/16): drop IE11? (acme/web)", State: "⬜ TODO in Decisions for you"},
		{Item: "Rotate the staging token (acme/web) [[2026-10-16#^rotate-token|^rotate-token]]", State: "TODO, manual action in Your actions"},
		{
			Item:  "[#12](https://github.com/acme/web/pull/12) login stack, in review (acme/web) [[2026-10-16#^login-stack|^login-stack]]",
			State: "🔍 IN_REVIEW in Agent queue",
		},
		{
			Item:  "Vendor credential for the v3 runner, asked Tuesday (acme/infra) [[2026-10-16#^vendor-cred|^vendor-cred]]",
			State: "⏳ WAITING in Outside input: chase",
		},
		// Carry-over: the deferred row and the one with no verdict, not the kept or dropped.
		{Item: "Bring the build cache online (acme/infra)", State: "offline"},
		{Item: `Write the onboarding doc \| draft`, State: "started"},
		// Process: the to-do and the unticked checkbox, not the ticked one.
		{Item: "File a ticket for the slow CI cache", State: "⬜ TODO in Process"},
		{Item: "Ask about the cache budget", State: "TODO, manual action in Process"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %d rows, want %d", len(got), len(want))
		for i := range max(len(got), len(want)) {
			var g, w note.CarryOverUnit
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if g != w {
				t.Errorf("row %d:\n got  %+v\n want %+v", i, g, w)
			}
		}
	}
}

func TestCarryOverNothingOpen(t *testing.T) {
	t.Parallel()
	src := []byte("# Worklog — 2026-10-16\n\n**18:00 UTC:** done.\n\n## Objectives\n\n" +
		"| Objective | Done when | Progress |\n|---|---|---|\n| A (o/r) | x | ✅ done |\n\n" +
		"## ✅ o/r\n\n### ✅ What remains\n\n#### ✅ Agent queue\n\n- ✅ Built ^built\n- [x] Pushed\n\n" +
		"## Carry-over\n\n| Item | State now | Verdict |\n|---|---|---|\n| Old | open | drop: gone |\n\n" +
		"## Process\n\n- —\n")
	prev, err := note.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if got, repos, err := carryOver(prev, "2026-10-16", note.Renderer{}); err != nil || len(got) != 0 || len(repos) != 0 {
		t.Fatalf("got %+v, %v, %v; want no rows", got, repos, err)
	}
}
