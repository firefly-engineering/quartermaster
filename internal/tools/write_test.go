package tools

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// Each write tool runs from the fixture note, as the day's note, to the expected bytes: the
// fixture with the tool's change spliced in, and nothing else.

// friday is the fixture's own day, at close.
var friday = time.Date(2026, 10, 16, 18, 5, 0, 0, time.UTC)

const fixtureDate = "2026-10-16"

// shellCommands sets up the ▶ buttons' command, with the id the fixture's buttons carry.
const shellCommands = `{"shell_commands":[{"id":"abc","platform_specific_commands":{"default":"quartermaster send {{vault_path}} {{_date}} {{_item}}"}}]}`

// fixtureDeps makes a vault holding the fixture note, on its own day, with buttons set up.
func fixtureDeps(t *testing.T, src string, jj bool) Deps {
	t.Helper()
	return newDeps(t, friday, map[string]string{fixtureDate + ".md": src, shellCommandsData: shellCommands}, jj)
}

// settled is the fixture as a write leaves it with no change of its own: two of its bucket
// icons are not the derived ones (spec §4.2).
func settled(t *testing.T) string {
	t.Helper()
	src := string(readFixture(t))
	for _, r := range [][2]string{{"#### ⬜ Your actions\n", yourActions}, {"#### 🔍 Agent queue\n", queue}} {
		if strings.Count(src, r[0]) != 1 {
			t.Fatalf("no %q in the fixture", r[0])
		}
		src = strings.Replace(src, r[0], r[1], 1)
	}
	return src
}

// edited is the settled fixture with each old string, which must occur once, replaced by its
// new one.
func edited(t *testing.T, oldNew ...string) string {
	t.Helper()
	src := settled(t)
	for i := 0; i < len(oldNew); i += 2 {
		if c := strings.Count(src, oldNew[i]); c != 1 {
			t.Fatalf("%q occurs %d times in the fixture", oldNew[i], c)
		}
		src = strings.Replace(src, oldNew[i], oldNew[i+1], 1)
	}
	return src
}

// checkNote compares the day's note with want, naming the first line they part on.
func (d Deps) checkNote(t *testing.T, want string) {
	t.Helper()
	if got := d.note(t, fixtureDate); got != want {
		t.Errorf("note differs:\n%s\ngot:\n%s", diffLine(want, got), got)
	}
}

func diffLine(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return "line " + strconv.Itoa(i+1) + ":\n want " + wl + "\n  got " + gl
		}
	}
	return ""
}

const (
	statusLine  = "**17:40 UTC, close:** Friday. Web stack in review; infra waits on a credential.\n"
	queue       = "#### 🔄 Agent queue\n"
	yourActions = "#### 🔄 Your actions\n"
)

func TestStatusSet(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), true)
	out, err := d.statusSet(t.Context(), StatusSetInput{Text: "Stack merged; infra still waits.", Label: "close"})
	if err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, statusLine, "**18:05 UTC, close:** Stack merged; infra still waits.\n"))
	if out.Status.Time != "18:05" || out.Status.Text != "**18:05 UTC, close:** Stack merged; infra still waits." ||
		!out.Committed || !out.Changed || out.Path == "" {
		t.Errorf("out %+v", out)
	}
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log, "worklog(2026-10-16): status_set: status at 18:05\n") {
		t.Errorf("commit %q", log)
	}

	// An explicit at wins over the clock; no label leaves it out.
	if _, err := d.statusSet(t.Context(), StatusSetInput{Text: "Closed.", At: "17:55"}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, statusLine, "**17:55 UTC:** Closed.\n"))

	for _, in := range []StatusSetInput{{Text: "x", At: "5pm"}, {Text: "x", At: "7:55"}, {Text: "a\nb"}, {Text: "x", Date: "16/10"}} {
		if _, err := d.statusSet(t.Context(), in); err == nil {
			t.Errorf("%+v: no error", in)
		}
	}
}

// TestStatusSetWritesMissing writes a status line under a title that has none.
func TestStatusSetWritesMissing(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, edited(t, statusLine+"\n", ""), false)
	if _, err := d.statusSet(t.Context(), StatusSetInput{Text: "Back."}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, statusLine, "**18:05 UTC:** Back.\n"))
}

func TestWriteNeedsANote(t *testing.T) {
	t.Parallel()
	d := newDeps(t, friday, nil, false)
	if _, err := d.statusSet(t.Context(), StatusSetInput{Text: "x"}); err == nil || !strings.Contains(err.Error(), "day_open") {
		t.Errorf("err %v", err)
	}
}

const (
	objective1 = "| Ship the login page (acme/web) | [#12](https://github.com/acme/web/issues/12) closed | 🔍 stack in review |\n"
	objective4 = "| Upgrade the runner (acme/infra) | runner on v3 | ⏳ waits on the vendor |\n"
)

func TestObjectiveSet(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	ctx := t.Context()

	out, err := d.objectiveSet(ctx, ObjectiveSetInput{Objective: "Close the epic (acme/web)", DoneWhen: "#20 | #21 closed"})
	if err != nil {
		t.Fatal(err)
	}
	added := objective4 + "| Close the epic (acme/web) | #20 \\| #21 closed | ⬜ |\n"
	d.checkNote(t, edited(t, objective4, added))
	if out.Row != 5 || out.Objective.State != "TODO" || out.Objective.DoneWhen != "#20 \\| #21 closed" {
		t.Errorf("add: %+v", out)
	}

	// Refine: a new done-condition, stamped with the clock's time and the reason.
	refined := "| Ship the login page (acme/web) (refined 18:05 UTC: the PR is the unit) | [#12](https://github.com/acme/web/pull/12) merged | 🔍 stack in review |\n"
	if _, err := d.objectiveSet(ctx, ObjectiveSetInput{
		Row: 1, DoneWhen: "[#12](https://github.com/acme/web/pull/12) merged", Reason: "the PR is the unit",
	}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, objective4, added, objective1, refined))

	// Refine again, at a time the user gave: the stamp is replaced, not stacked.
	reworded := "| Ship the login page and its tests (acme/web) (refined 17:50 UTC: tests count) | [#12](https://github.com/acme/web/pull/12) merged | 🔍 stack in review |\n"
	if _, err := d.objectiveSet(ctx, ObjectiveSetInput{
		Row: 1, Objective: "Ship the login page and its tests (acme/web)", Reason: "tests count", At: "17:50",
	}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, objective4, added, objective1, reworded))

	// Settle: the stamp stays when the objective does not change.
	out, err = d.objectiveSet(ctx, ObjectiveSetInput{Row: 1, State: "DONE", Progress: "merged"})
	if err != nil {
		t.Fatal(err)
	}
	settled := strings.Replace(reworded, "🔍 stack in review", "✅ merged", 1)
	d.checkNote(t, edited(t, objective4, added, objective1, settled))
	if out.Row != 1 || out.Objective.State != "DONE" {
		t.Errorf("settle: %+v", out)
	}

	for _, in := range []ObjectiveSetInput{
		{Row: 4, DoneWhen: "runner on v4"},                   // a refinement needs its reason
		{Row: 4, Reason: "why"},                              // a reason with nothing refined
		{Row: 4, State: "DONE", Progress: "v3", At: "17:00"}, // an at with nothing refined
		{Row: 4},                 // nothing to set
		{Row: 9, State: "DONE"},  // no such row
		{Row: -1, State: "DONE"}, // no such row
		{Row: 2, State: "DONE!"}, // no such state
		{Objective: "x (o/r)"},   // a new one needs done_when
		{Objective: "x (o/r)", DoneWhen: "y", State: "DROPPED"}, // a drop says why
		{Objective: "x (o/r)", DoneWhen: "y", Reason: "z"},      // nothing to refine
	} {
		if _, err := d.objectiveSet(ctx, in); err == nil {
			t.Errorf("%+v: no error", in)
		}
	}
	d.checkNote(t, edited(t, objective4, added, objective1, settled))
}

const (
	sentBucket = "#### — Sent to other repos\n\n- —\n"
	infraChase = "#### ⏳ Outside input: chase\n\n- ⏳ Vendor credential for the v3 runner, asked Tuesday ^vendor-cred\n"
	loginStack = "- 🔍 [#12](https://github.com/acme/web/pull/12) login stack, in review ^login-stack\n"
	wave2      = "- Wave 2 after the stack lands.\n"
)

// grant is a ##### work item with a prompt, sent to another repo.
var grant = ItemFields{
	ID: "grant", Repo: "acme/web", Bucket: "Sent to other repos", Title: "grant the deploy role", Ref: "acme/IaC#7",
	Body: "Needed for #12.", Cost: "10 min", Route: "/implement", Prompt: &PromptInput{To: "IaC", Text: "Grant the deploy role."},
}

const grantItem = "##### ⬜ [IaC#7](https://github.com/acme/IaC/issues/7): grant the deploy role\n\n" +
	"Needed for #12. 10 min. Route: `/implement`.\n" +
	"[▶ send to IaC](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-16&_item=grant) ^grant\n\n" +
	"> [!prompt]- Prompt sent to IaC\n> ```herdr-prompt id=grant to=IaC\n> Grant the deploy role.\n> ```\n"

func TestItemUpsertCreate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   ItemUpsertInput
		want []string // old, new pairs over the fixture
	}{
		{"a ##### item in place of a bucket's dash", ItemUpsertInput{ItemFields: grant},
			[]string{sentBucket, "#### ⬜ Sent to other repos\n\n" + grantItem}},
		{"a list item joins the bucket's last list", ItemUpsertInput{
			ItemFields: ItemFields{ID: "flaky", Repo: "acme/web", Bucket: "Agent queue", Title: "flaky test", Ref: "#9", Route: "/implement"},
		}, []string{wave2, wave2 + "- ⬜ [#9](https://github.com/acme/web/issues/9): flaky test · Route: `/implement` ^flaky\n"}},
		{"a missing bucket, in template order", ItemUpsertInput{
			ItemFields: ItemFields{ID: "cache", Repo: "acme/infra", Bucket: "Waiting", Title: "cache budget", Cost: "5 min"},
			State:      "WAITING",
		}, []string{infraChase, infraChase + "\n#### ⏳ Waiting\n\n- ⏳ cache budget · 5 min ^cache\n"}},
		{"a missing project, after the last one", ItemUpsertInput{
			ItemFields: ItemFields{ID: "bump", Repo: "acme/api", Bucket: "Agent queue", Title: "bump Go", Ref: "acme/api#3"},
		}, []string{infraChase, infraChase + "\n## ⬜ acme/api\n\n### ⬜ What remains\n\n#### — Decisions for you\n\n- —\n\n" +
			"#### — Your actions\n\n- —\n\n#### ⬜ Agent queue\n\n- ⬜ [#3](https://github.com/acme/api/issues/3): bump Go ^bump\n\n" +
			"#### — Sent to other repos\n\n- —\n\n#### — Outside input: chase\n\n- —\n\n#### — Waiting\n\n- —\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, settled(t), false)
			out, err := d.itemUpsert(t.Context(), c.in)
			if err != nil {
				t.Fatal(err)
			}
			d.checkNote(t, edited(t, c.want...))
			if !out.Created || out.Item.ID != c.in.ID || out.Repo != c.in.Repo || out.Bucket != c.in.Bucket {
				t.Errorf("out %+v", out)
			}
		})
	}
}

func TestItemUpsertUpdate(t *testing.T) {
	t.Parallel()
	t.Run("a list item in place, keeping its state", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.itemUpsert(t.Context(), ItemUpsertInput{ItemFields: ItemFields{ID: "login-stack", Title: "login stack", Ref: "#12", Cost: "20 min"}})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, loginStack, "- 🔍 [#12](https://github.com/acme/web/issues/12): login stack · 20 min ^login-stack\n"))
		if out.Created || out.Item.State != "IN_REVIEW" || out.Bucket != "Agent queue" {
			t.Errorf("out %+v", out)
		}
	})
	t.Run("a ##### item in place", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.itemUpsert(t.Context(), ItemUpsertInput{ItemFields: ItemFields{
			ID: "session-store", Title: "which session store?", Ref: "#14", Body: "Unblocks #15.", Cost: "15 min",
			Route: "/grill-with-docs", Button: "grill in web", Prompt: &PromptInput{To: "web", Text: "/grill-with-docs https://github.com/acme/web/issues/14"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, "Unblocks #15. 10 min.", "Unblocks #15. 15 min."))
		if !out.Changed || out.Item.Prompt == nil || out.Item.Prompt.To != "web" {
			t.Errorf("out %+v", out)
		}
	})
	t.Run("keeping its dispatch marker, in either form", func(t *testing.T) {
		t.Parallel()
		const marker = "<!-- qm pane=w1:p2 ws=/ws/acme/web/issue-12 -->"
		withMarker := func(src string) string {
			src = strings.Replace(src, "in review ^login-stack", "in review "+marker+" ^login-stack", 1)
			return strings.Replace(src, "&_item=session-store) ^session-store", "&_item=session-store) "+marker+" ^session-store", 1)
		}
		d := fixtureDeps(t, withMarker(settled(t)), false)
		for _, f := range []ItemFields{{ID: "login-stack", Title: "login stack, in review", Ref: "acme/web#12"}, {
			ID: "session-store", Title: "which session store?", Ref: "#14", Body: "Unblocks #15.", Cost: "15 min",
			Route: "/grill-with-docs", Button: "grill in web", Prompt: &PromptInput{To: "web", Text: "/grill-with-docs https://github.com/acme/web/issues/14"},
		}} {
			if _, err := d.itemUpsert(t.Context(), ItemUpsertInput{ItemFields: f}); err != nil {
				t.Fatal(err)
			}
		}
		d.checkNote(t, withMarker(edited(t, "Unblocks #15. 10 min.", "Unblocks #15. 15 min.",
			loginStack, "- 🔍 [#12](https://github.com/acme/web/issues/12): login stack, in review ^login-stack\n")))
	})
	t.Run("moved to another project's missing bucket", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.itemUpsert(t.Context(), ItemUpsertInput{ItemFields: ItemFields{
			ID: "vendor-cred", Bucket: "Waiting", Title: "Vendor credential for the v3 runner, asked Tuesday",
		}})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, infraChase,
			"#### — Outside input: chase\n\n- —\n\n#### ⏳ Waiting\n\n- ⏳ Vendor credential for the v3 runner, asked Tuesday ^vendor-cred\n"))
		if out.Bucket != "Waiting" || out.Repo != "acme/infra" || out.Item.State != "WAITING" {
			t.Errorf("out %+v", out)
		}
	})
}

func TestItemUpsertRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in  ItemUpsertInput
		msg string
	}{
		{ItemUpsertInput{ItemFields: ItemFields{ID: "login-stack", Title: "x"}, State: "DONE"}, "item_transition"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "new", Repo: "acme/web", Title: "x"}}, "bucket"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "new", Repo: "acme/web", Bucket: "Someday", Title: "x"}}, "unknown bucket"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "rotate-token", Repo: "acme/web", Bucket: "Your actions", Title: "x"}}, "manual action"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "New", Repo: "acme/web", Bucket: "Waiting", Title: "x"}}, "item id"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "new", Repo: "acme/web", Bucket: "Waiting", Title: "a — b"}}, SummarySepText},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "new", Repo: "acme/web", Bucket: "Waiting", Title: "x", Ref: "12"}}, "owner/repo#N"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "new", Repo: "acme/web", Bucket: "Waiting", Title: "x", Button: "go"}}, "prompt"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "login-stack"}}, "title"},
		// An update may not drop a ##### item's prose or prompt by leaving them out.
		{ItemUpsertInput{ItemFields: ItemFields{ID: "session-store", Title: "x"}}, "##### item"},
		{ItemUpsertInput{ItemFields: ItemFields{ID: "session-store", Title: "x", Body: "y"}}, "prompt"},
	} {
		if _, err := d.itemUpsert(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.msg)
		}
	}
	d.checkNote(t, settled(t))
}

// SummarySepText is the separator as an error quotes it.
const SummarySepText = `" — "`

func TestItemTransition(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), true)
	out, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "login-stack", To: "DONE", Summary: "merged in abc123"})
	if err != nil {
		t.Fatal(err)
	}
	// The queue's last open item is done: its heading is derived anew.
	d.checkNote(t, edited(t, queue, "#### ✅ Agent queue\n",
		loginStack, "- ✅ [#12](https://github.com/acme/web/pull/12) login stack, in review — merged in abc123 ^login-stack\n"))
	if out.From != "IN_REVIEW" || out.To != "DONE" || out.Item == nil || out.Item.Item.State != "DONE" || out.Action != nil {
		t.Errorf("out %+v", out)
	}
	if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
		"worklog(2026-10-16): item_transition: ^login-stack IN_REVIEW to DONE: merged in abc123\n") {
		t.Errorf("commit %q", log)
	}
}

// TestItemTransitionTicks ticks a manual action, and the Next line paired with it.
func TestItemTransitionTicks(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	out, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "rotate-token", To: "DONE"})
	if err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t,
		"1. [ ] Rotate the staging token", "1. [x] Rotate the staging token",
		"#### 🔄 Your actions\n\n- [ ] Rotate", "#### ✅ Your actions\n\n- [x] Rotate"))
	if out.Action == nil || !out.Action.Checked || out.Item != nil || out.From != "TODO" {
		t.Errorf("out %+v", out)
	}
}

func TestItemTransitionRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in  ItemTransitionInput
		err error
	}{
		{ItemTransitionInput{Item: "login-stack", To: "WAITING"}, note.ErrReasonRequired},
		{ItemTransitionInput{Item: "session-store", To: "IN_REVIEW"}, note.ErrIllegalTransition},
		{ItemTransitionInput{Item: "old-form", To: "TODO"}, note.ErrIllegalTransition},
		{ItemTransitionInput{Item: "nope", To: "DONE"}, note.ErrNoItem},
		{ItemTransitionInput{Item: "login-stack", To: "done"}, nil},
	} {
		if _, err := d.itemTransition(t.Context(), c.in); err == nil || c.err != nil && !errors.Is(err, c.err) {
			t.Errorf("%+v: %v, want %v", c.in, err, c.err)
		}
	}
	d.checkNote(t, settled(t))
}

const next = "1. [ ] Rotate the staging token · 5 min · [[#^rotate-token|details]]\n" +
	"2. Review the login stack · 20 min · [[#^login-stack|details]]\n" +
	"3. [ ] Call the vendor · 10 min\n"

func TestNextSet(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	out, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{
		{ID: "session-store", Cost: "10 min"},
		{ID: "rotate-token", Cost: "5 min"},
		{ID: "login-stack", Action: "Review the login stack", Cost: "20 min"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, next,
		"1. [#14](https://github.com/acme/web/issues/14): which session store? · 10 min · [[#^session-store|details]] · "+
			"[▶ grill in web](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-16&_item=session-store)\n"+
			"2. [ ] Rotate the staging token · 5 min · [[#^rotate-token|details]]\n"+
			"3. Review the login stack · 20 min · [[#^login-stack|details]]\n"))
	if len(out.Next) != 3 || out.Next[1].Box != "unchecked" || out.Next[0].Ref != "session-store" {
		t.Errorf("next %+v", out.Next)
	}
	if len(out.Dropped) != 1 || out.Dropped[0].Text != "3. [ ] Call the vendor · 10 min" {
		t.Errorf("dropped %+v", out.Dropped)
	}

	if _, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{}}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, "## Next\n\n"+next, "## Next\n"))
}

// TestNextSetKeepsTicks: a Next line the user ticked in Obsidian is written ticked when
// next_set keeps its item, and ticks its manual action when next_set drops it, so the untick
// guard has nothing to refuse.
func TestNextSetKeepsTicks(t *testing.T) {
	t.Parallel()
	ticked := edited(t, "1. [ ] Rotate", "1. [x] Rotate")
	t.Run("kept", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, ticked, false)
		if _, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{{ID: "rotate-token"}, {ID: "login-stack"}}}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, next,
			"1. [x] Rotate the staging token · [[#^rotate-token|details]]\n"+
				"2. [#12](https://github.com/acme/web/pull/12) login stack, in review · [[#^login-stack|details]]\n",
			"#### 🔄 Your actions\n\n- [ ] Rotate", "#### ✅ Your actions\n\n- [x] Rotate"))
	})
	t.Run("dropped", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, ticked, false)
		if _, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{{ID: "login-stack", Action: "Review"}}}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, next, "1. Review · [[#^login-stack|details]]\n",
			"#### 🔄 Your actions\n\n- [ ] Rotate", "#### ✅ Your actions\n\n- [x] Rotate"))
	})
}

func TestNextSetRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	six := make([]NextInput, 6)
	for i := range six {
		six[i] = NextInput{ID: "login-stack"}
	}
	for _, c := range []struct {
		in  []NextInput
		msg string
	}{
		{six, "at most 5"},
		{[]NextInput{{ID: "login-stack"}, {ID: "login-stack"}}, "twice"},
		{[]NextInput{{ID: "nope"}}, "names no"},
		{[]NextInput{{ID: "login-stack", Action: "a\nb"}}, "one line"},
	} {
		if _, err := d.nextSet(t.Context(), NextSetInput{Items: c.in}); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.msg)
		}
	}
	d.checkNote(t, settled(t))
}

func TestNextSetAnchor(t *testing.T) {
	t.Parallel()
	src := edited(t, "- Wave 2 after the stack lands.\n", "- Wave 2 after the stack lands.\n\nThe queue. ^queue\n")
	d := fixtureDeps(t, src, false)
	if _, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{{ID: "queue"}}}); err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Errorf("an anchor without its action: %v", err)
	}
	if _, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{{ID: "queue", Action: "Read the queue"}}}); err != nil {
		t.Fatal(err)
	}
	if got := d.note(t, fixtureDate); !strings.Contains(got, "## Next\n\n1. Read the queue · [[#^queue|details]]\n\n## ") {
		t.Errorf("note:\n%s", got)
	}
}

const (
	row1 = "| Bring the build cache online (acme/infra) | offline | defer: hardware |\n"
	row2 = "| [#9](https://github.com/acme/web/issues/9) flaky test | open | keep |\n"
	row5 = "| Write the onboarding doc \\| draft | started | |\n"
)

func TestCarryOverVerdict(t *testing.T) {
	t.Parallel()
	t.Run("defer", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.carryOverVerdict(t.Context(), CarryOverVerdictInput{Row: 1, Verdict: "defer", Reason: "hardware still out"})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, row1, "| Bring the build cache online (acme/infra) | offline | defer: hardware still out |\n"))
		if out.Row.Verdict != "defer: hardware still out" || out.Kept != nil {
			t.Errorf("out %+v", out)
		}
	})
	t.Run("drop, a pipe in the item kept escaped", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.carryOverVerdict(t.Context(), CarryOverVerdictInput{Row: 5, Verdict: "drop", Reason: "superseded"}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, row5, "| Write the onboarding doc \\| draft | started | drop: superseded |\n"))
	})
	t.Run("keep moves it into its bucket", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.carryOverVerdict(t.Context(), CarryOverVerdictInput{Row: 2, Verdict: "keep", Reason: "in scope",
			Item: &ItemFields{ID: "flaky", Repo: "acme/web", Bucket: "Agent queue", Route: "/implement"}})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t,
			wave2, wave2+"- ⬜ [#9](https://github.com/acme/web/issues/9) flaky test · Route: `/implement` ^flaky\n",
			row2, "| [#9](https://github.com/acme/web/issues/9) flaky test | open | keep: in scope |\n"))
		if out.Kept == nil || out.Kept.Item.ID != "flaky" || out.Kept.Bucket != "Agent queue" {
			t.Errorf("out %+v", out)
		}
	})
	t.Run("keep of an item already in the note records the verdict", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.carryOverVerdict(t.Context(), CarryOverVerdictInput{Row: 1, Verdict: "keep",
			Item: &ItemFields{ID: "vendor-cred"}}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, row1, "| Bring the build cache online (acme/infra) | offline | keep |\n"))
	})
	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		for _, in := range []CarryOverVerdictInput{
			{Row: 1, Verdict: "keep"},
			{Row: 1, Verdict: "defer", Item: &ItemFields{ID: "x"}},
			{Row: 1, Verdict: "drop"},
			{Row: 1, Verdict: "maybe"},
			{Row: 6, Verdict: "defer"},
			{Row: 0, Verdict: "defer"},
			{Row: 1, Verdict: "keep", Item: &ItemFields{ID: "x", Repo: "acme/infra"}},
		} {
			if _, err := d.carryOverVerdict(t.Context(), in); err == nil {
				t.Errorf("%+v: no error", in)
			}
		}
		d.checkNote(t, settled(t))
	})
}
