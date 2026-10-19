package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

const (
	part1 = "### ✅ Part 1 — Agent batch: login page, done\n\n" +
		"- ✅ [#12](https://github.com/acme/web/issues/12) login form, PR up.\n- [ ] Check the preview deploy by hand\n"
	part1End    = "- [ ] Check the preview deploy by hand\n"
	nextLogin   = "2. Review the login stack · 20 min · [[#^login-stack|details]]\n"
	infraRemain = "## ⏳ acme/infra\n\n### ⏳ What remains\n"
	processEnd  = "- [x] Clean the stale workspaces\n"
	sessionItem = "##### ⬜ [#14](https://github.com/acme/web/issues/14): which session store?\n\n" +
		"Unblocks #15. Cost: 10 min. Route: `/grill-with-docs`.\n" +
		"[▶ grill in web](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-16&_item=session-store) ^session-store\n\n" +
		"> [!prompt]- Prompt sent to web\n> ```herdr-prompt id=session-store to=web\n" +
		"> /grill-with-docs https://github.com/acme/web/issues/14\n> ```\n\n"
)

func TestPartWrite(t *testing.T) {
	t.Parallel()
	t.Run("a new Part, after the last one", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), true)
		out, err := d.partWrite(t.Context(), PartWriteInput{
			Repo: "acme/web", N: 2, Kind: "Land", Title: "stack merged", Icon: "✅", Body: "- merged [#12](https://github.com/acme/web/pull/12)\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, part1End, part1End+"\n### ✅ Part 2 — Land: stack merged\n\n- merged [#12](https://github.com/acme/web/pull/12)\n"))
		if !out.Created || out.Repo != "acme/web" || out.Part.N != 2 || out.Part.Icon != "✅" ||
			out.Part.Body != "- merged [#12](https://github.com/acme/web/pull/12)" {
			t.Errorf("out %+v", out)
		}
		if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
			"worklog(2026-10-16): part_write: add acme/web, Part 2 — Land\n") {
			t.Errorf("commit %q", log)
		}
	})
	t.Run("a Part rewritten whole, with an anchor", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		out, err := d.partWrite(t.Context(), PartWriteInput{
			Repo: "acme/web", N: 1, Kind: "Agent batch", Title: "login page, in review", Icon: "🔍", Body: "Wave 1 is up. ^wave1",
		})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, part1, "### 🔍 Part 1 — Agent batch: login page, in review\n\nWave 1 is up. ^wave1\n"))
		if out.Created || out.Part.Title != "login page, in review" {
			t.Errorf("out %+v", out)
		}
		// Next may point at the anchor now.
		if _, err := d.nextSet(t.Context(), NextSetInput{Items: []NextInput{{ID: "wave1", Action: "Review wave 1"}}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("the first Part of a project, before What remains", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.partWrite(t.Context(), PartWriteInput{Repo: "acme/infra", N: 1, Kind: "Human session", Title: "unblock", Icon: "🔄"}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, infraRemain, "## ⏳ acme/infra\n\n### 🔄 Part 1 — Human session: unblock\n\n### ⏳ What remains\n"))
	})
}

func TestPartWriteRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	ok := PartWriteInput{Repo: "acme/web", N: 2, Kind: "Land", Icon: "✅"}
	with := func(f func(*PartWriteInput)) PartWriteInput { in := ok; f(&in); return in }
	for _, c := range []struct {
		in  PartWriteInput
		msg string
	}{
		{with(func(in *PartWriteInput) { in.Icon = "⬜" }), "part icon"},
		{with(func(in *PartWriteInput) { in.Kind = "Meeting" }), "part kind"},
		{with(func(in *PartWriteInput) { in.N = 0 }), "part number"},
		{with(func(in *PartWriteInput) { in.Repo = "acme/api" }), "item_upsert"},
		{with(func(in *PartWriteInput) { in.Title = "a\nb" }), "one line"},
		{with(func(in *PartWriteInput) { in.Body = "Done.\n\n## Elsewhere\n" }), "no heading"},
		{with(func(in *PartWriteInput) { in.Body = "- ✅ shipped ^shipped\n" }), "work item"},
		{with(func(in *PartWriteInput) { in.Body = "Again. ^login-stack\n" }), "duplicate"},
		// A box whose id the note holds elsewhere: action_upsert moves a manual action.
		{PartWriteInput{Repo: "acme/web", N: 1, Kind: "Land", Icon: "✅", Body: "- [ ] Approve the design doc ^design-doc"}, "duplicate"},
	} {
		if _, err := d.partWrite(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.msg)
		}
	}
	d.checkNote(t, settled(t))
}

// TestPartWriteKeepsTicks: a rewrite that unticks a box ticked in the Part is refused.
func TestPartWriteKeepsTicks(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, edited(t, "- [ ] Check the preview deploy by hand", "- [x] Check the preview deploy by hand"), false)
	_, err := d.partWrite(t.Context(), PartWriteInput{Repo: "acme/web", N: 1, Kind: "Agent batch", Icon: "✅", Body: "- [ ] Check the preview deploy by hand"})
	if !errors.Is(err, engine.ErrUntick) {
		t.Errorf("err %v, want %v", err, engine.ErrUntick)
	}
}

func TestItemTransitionIntoPart(t *testing.T) {
	t.Parallel()
	t.Run("a list item", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), true)
		out, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "login-stack", To: "DONE", Summary: "merged in abc123", Part: 1})
		if err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, nextLogin, "", loginStack, "", queue, "#### ✅ Agent queue\n",
			part1End, part1End+"- ✅ [#12](https://github.com/acme/web/pull/12) login stack, in review — merged in abc123\n"))
		if out.From != "IN_REVIEW" || out.To != "DONE" || out.Item != nil || out.Part == nil || out.Part.Repo != "acme/web" || out.Part.Part.N != 1 {
			t.Errorf("out %+v", out)
		}
		if log := d.jj(t, "log", "--no-graph", "-r", "@-", "-T", "description"); !strings.HasPrefix(log,
			"worklog(2026-10-16): item_transition: ^login-stack IN_REVIEW to DONE: merged in abc123, into Part 1\n") {
			t.Errorf("commit %q", log)
		}
	})
	t.Run("a ##### item, without its prose, button and prompt", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "session-store", To: "DROPPED", Summary: "settled on #17", Part: 1}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, sessionItem, "", "#### ⬜ Decisions for you", "#### — Decisions for you",
			part1End, part1End+"- ❌ [#14](https://github.com/acme/web/issues/14): which session store? — settled on #17\n"))
	})
	t.Run("into a Part with no lines yet", func(t *testing.T) {
		t.Parallel()
		d := fixtureDeps(t, settled(t), false)
		if _, err := d.partWrite(t.Context(), PartWriteInput{Repo: "acme/infra", N: 1, Kind: "Human session", Icon: "🔄"}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "vendor-cred", To: "DROPPED", Summary: "no longer needed", Part: 1}); err != nil {
			t.Fatal(err)
		}
		d.checkNote(t, edited(t, infraRemain+"\n#### ⏳ Outside input: chase\n\n- ⏳ Vendor credential for the v3 runner, asked Tuesday ^vendor-cred\n",
			"## 🔄 acme/infra\n\n### 🔄 Part 1 — Human session\n\n- ❌ Vendor credential for the v3 runner, asked Tuesday — no longer needed\n\n"+
				"### — What remains\n\n#### — Outside input: chase\n\n- —\n"))
	})
}

// TestItemTransitionFilesFinished files an item already DONE or DROPPED into a Part with a
// move to its own state (spec §6): it keeps its last summary unless given one.
func TestItemTransitionFilesFinished(t *testing.T) {
	t.Parallel()
	const title = "[#12](https://github.com/acme/web/pull/12) login stack, in review"
	for _, c := range []struct {
		name, to, icon, last, summary, want string
	}{
		{"DONE, its last summary kept", "DONE", "✅", "merged in abc123", "", "merged in abc123"},
		{"DROPPED, its last summary kept", "DROPPED", "❌", "superseded by #18", "", "superseded by #18"},
		{"DONE, a summary given", "DONE", "✅", "merged in abc123", "landed in main", "landed in main"},
		{"DROPPED, a summary given", "DROPPED", "❌", "superseded by #18", "folded into #18", "folded into #18"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, settled(t), true)
			if _, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "login-stack", To: c.to, Summary: c.last}); err != nil {
				t.Fatal(err)
			}
			if _, err := d.partWrite(t.Context(), PartWriteInput{Repo: "acme/web", N: 2, Kind: "Land", Icon: "✅"}); err != nil {
				t.Fatal(err)
			}
			before := len(d.commits(t))
			out, err := d.itemTransition(t.Context(), ItemTransitionInput{Item: "login-stack", To: c.to, Summary: c.summary, Part: 2})
			if err != nil {
				t.Fatal(err)
			}
			if c := d.commits(t); len(c)-before != 1 || !strings.HasSuffix(c[0], ", into Part 2") {
				t.Errorf("commits %q, %d before", c, before)
			}
			d.checkNote(t, edited(t, nextLogin, "", loginStack, "", queue, "#### ✅ Agent queue\n",
				part1End, part1End+"\n### ✅ Part 2 — Land\n\n- "+c.icon+" "+title+" — "+c.want+"\n"))
			if out.From != c.to || out.To != c.to || out.Item != nil || out.Part == nil || out.Part.Part.N != 2 {
				t.Errorf("out %+v", out)
			}
		})
	}
}

func TestItemTransitionIntoPartRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in  ItemTransitionInput
		err error
		msg string
	}{
		{ItemTransitionInput{Item: "login-stack", To: "DONE", Part: 9}, ErrNoPart, "part_write"},
		{ItemTransitionInput{Item: "vendor-cred", To: "DONE", Summary: "x", Part: 1}, ErrNoPart, "acme/infra has no Part 1"},
		{ItemTransitionInput{Item: "login-stack", To: "IN_PROGRESS", Part: 1}, nil, "DONE or DROPPED"},
		{ItemTransitionInput{Item: "login-stack", To: "IN_REVIEW", Summary: "x", Part: 1}, nil, "DONE or DROPPED"},
		{ItemTransitionInput{Item: "rotate-token", To: "DONE", Part: 1}, nil, "manual action"},
		{ItemTransitionInput{Item: "nope", To: "DONE", Part: 1}, note.ErrNoItem, ""},
		{ItemTransitionInput{Item: "old-form", To: "DONE", Part: 1}, note.ErrIllegalTransition, ""},
		{ItemTransitionInput{Item: "session-store", To: "DROPPED", Part: 1}, note.ErrReasonRequired, ""},
	} {
		_, err := d.itemTransition(t.Context(), c.in)
		if err == nil || c.err != nil && !errors.Is(err, c.err) || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%+v: %v, want %v %q", c.in, err, c.err, c.msg)
		}
	}
	d.checkNote(t, settled(t))
}

func TestActionUpsert(t *testing.T) {
	t.Parallel()
	const (
		rotate = "- [ ] Rotate the staging token ^rotate-token\n"
		design = "- [x] Approve the design doc ^design-doc\n"
	)
	cases := []struct {
		name  string
		in    ActionUpsertInput
		want  []string
		where string
	}{
		{"created in Your actions", ActionUpsertInput{ID: "grant", Text: "Grant the deploy role", Repo: "acme/web"},
			[]string{design, design + "- [ ] Grant the deploy role ^grant\n"}, "acme/web, Your actions"},
		{"created in a missing bucket", ActionUpsertInput{ID: "call", Text: "Call the vendor", Repo: "acme/infra"},
			[]string{infraRemain, infraRemain + "\n#### ⬜ Your actions\n\n- [ ] Call the vendor ^call\n"}, "acme/infra, Your actions"},
		{"created in a Part", ActionUpsertInput{ID: "preview", Text: "Check prod too", Repo: "acme/web", Part: 1},
			[]string{part1End, part1End + "- [ ] Check prod too ^preview\n"}, "acme/web, Part 1"},
		{"created in Process", ActionUpsertInput{ID: "budget", Text: "Agree the cache budget", Process: true},
			[]string{processEnd, processEnd + "- [ ] Agree the cache budget ^budget\n"}, "Process"},
		{"reworded in place", ActionUpsertInput{ID: "rotate-token", Text: "Rotate the staging and prod tokens"},
			[]string{rotate, "- [ ] Rotate the staging and prod tokens ^rotate-token\n"}, "acme/web, Your actions"},
		{"reworded, its place named as it is", ActionUpsertInput{ID: "rotate-token", Text: "Rotate it", Repo: "acme/web", Bucket: "Your actions"},
			[]string{rotate, "- [ ] Rotate it ^rotate-token\n"}, "acme/web, Your actions"},
		{"moved, ticked as it was", ActionUpsertInput{ID: "design-doc", Process: true},
			[]string{design, "", yourActions, "#### ⬜ Your actions\n", processEnd, processEnd + design}, "Process"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := fixtureDeps(t, settled(t), false)
			out, err := d.actionUpsert(t.Context(), c.in)
			if err != nil {
				t.Fatal(err)
			}
			d.checkNote(t, edited(t, c.want...))
			if out.Where != c.where || out.Action.ID != c.in.ID || out.Created != strings.HasPrefix(c.name, "created") {
				t.Errorf("out %+v", out)
			}
		})
	}
}

func TestActionUpsertRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in  ActionUpsertInput
		msg string
	}{
		{ActionUpsertInput{ID: "new", Repo: "acme/web"}, "needs its text"},
		{ActionUpsertInput{ID: "new", Text: "x"}, "needs its place"},
		{ActionUpsertInput{ID: "New", Text: "x", Repo: "acme/web"}, "id"},
		{ActionUpsertInput{ID: "login-stack", Text: "x", Repo: "acme/web"}, "work item"},
		{ActionUpsertInput{ID: "new", Text: "x", Repo: "acme/web", Bucket: "Waiting", Part: 1}, "not both"},
		{ActionUpsertInput{ID: "new", Text: "x", Repo: "acme/web", Part: 9}, "part_write"},
		{ActionUpsertInput{ID: "new", Text: "x", Repo: "acme/web", Bucket: "Someday"}, "unknown bucket"},
		{ActionUpsertInput{ID: "new", Text: "x", Repo: "acme/api"}, "item_upsert"},
		{ActionUpsertInput{ID: "new", Text: "x", Repo: "acme/web", Process: true}, "Process"},
		{ActionUpsertInput{ID: "new", Text: "✅ x", Repo: "acme/web"}, "status icon"},
		{ActionUpsertInput{ID: "rotate-token"}, "nothing to change"},
	} {
		if _, err := d.actionUpsert(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.msg)
		}
	}
	d.checkNote(t, settled(t))
}

func TestProcessSet(t *testing.T) {
	t.Parallel()
	const first = "- File a ticket for the slow CI cache\n"
	d := fixtureDeps(t, settled(t), false)
	ctx := t.Context()
	out, err := d.processSet(ctx, ProcessSetInput{Text: "Keep PRs small"})
	if err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, processEnd, processEnd+"- Keep PRs small\n"))
	if len(out.Process) != 2 || out.Process[1].Text != "Keep PRs small" {
		t.Errorf("out %+v", out)
	}
	if _, err := d.processSet(ctx, ProcessSetInput{Entry: 1, Text: "Ticket the slow CI cache"}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, processEnd, processEnd+"- Keep PRs small\n", first, "- Ticket the slow CI cache\n"))
	if _, err := d.processSet(ctx, ProcessSetInput{Entry: 1, Settle: true}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, processEnd, processEnd+"- Keep PRs small\n", first, ""))

	for _, in := range []ProcessSetInput{
		{Entry: 5, Text: "x"}, {Settle: true}, {Entry: 1, Settle: true, Text: "x"}, {Text: " "}, {Text: "[ ] a box"}, {Entry: -1, Text: "x"},
	} {
		if _, err := d.processSet(ctx, in); err == nil {
			t.Errorf("%+v: no error", in)
		}
	}
	d.checkNote(t, edited(t, processEnd, processEnd+"- Keep PRs small\n", first, ""))
}

// TestProcessSetDash adds an entry in place of Process's `- —`, and settles it back.
func TestProcessSetDash(t *testing.T) {
	t.Parallel()
	const process = "## Process\n\n- File a ticket for the slow CI cache\n- [ ] Ask about the cache budget\n" + processEnd
	d := fixtureDeps(t, edited(t, process, "## Process\n\n- —\n"), false)
	if _, err := d.processSet(t.Context(), ProcessSetInput{Text: "Keep PRs small"}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, process, "## Process\n\n- Keep PRs small\n"))
	if _, err := d.processSet(t.Context(), ProcessSetInput{Entry: 1, Settle: true}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, process, "## Process\n\n- —\n"))
}

func TestItemUpsertSent(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	ctx := t.Context()
	f := grant
	f.Sent = "to send"
	if _, err := d.itemUpsert(ctx, ItemUpsertInput{ItemFields: f}); err != nil {
		t.Fatal(err)
	}
	head := "##### ⬜ [IaC#7](https://github.com/acme/IaC/issues/7): grant the deploy role\n"
	sent := func(status string) string {
		return edited(t, sentBucket, "#### ⬜ Sent to other repos\n\n"+strings.Replace(grantItem, head,
			"##### ⬜ [IaC#7](https://github.com/acme/IaC/issues/7): grant the deploy role → IaC, **"+status+"**\n", 1))
	}
	d.checkNote(t, sent("to send"))

	// An update without a status keeps it; one with a status writes it.
	if _, err := d.itemUpsert(ctx, ItemUpsertInput{ItemFields: grant}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, sent("to send"))
	f.Sent = "sent"
	out, err := d.itemUpsert(ctx, ItemUpsertInput{ItemFields: f})
	if err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, sent("sent"))
	if s := out.Item.Sent; s == nil || s.Status != "sent" || s.To != "IaC" {
		t.Errorf("sent %+v", s)
	}

	for _, c := range []struct {
		f   ItemFields
		msg string
	}{
		{ItemFields{ID: "w", Repo: "acme/web", Bucket: "Waiting", Title: "x", Sent: "sent", SentTo: "IaC"}, "Sent to other repos"},
		{ItemFields{ID: "w", Repo: "acme/web", Bucket: "Sent to other repos", Title: "x", Sent: "posted", SentTo: "IaC"}, "sent status"},
		{ItemFields{ID: "w", Repo: "acme/web", Bucket: "Sent to other repos", Title: "x", SentTo: "IaC"}, "sent_to"},
		{ItemFields{ID: "w", Repo: "acme/web", Bucket: "Sent to other repos", Title: "x", Sent: "sent"}, "sent_to"},
		{ItemFields{ID: "w", Repo: "acme/web", Bucket: "Sent to other repos", Title: "x → IaC, **sent**", Sent: "sent", SentTo: "IaC"}, "already"},
	} {
		if _, err := d.itemUpsert(ctx, ItemUpsertInput{ItemFields: c.f}); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%+v: %v, want %q", c.f, err, c.msg)
		}
	}
	d.checkNote(t, sent("sent"))

	// Filed into a Part, a done ask leaves its sent status with its id, prose and prompt.
	if _, err := d.itemTransition(ctx, ItemTransitionInput{Item: "grant", To: "DONE", Summary: "granted", Part: 1}); err != nil {
		t.Fatal(err)
	}
	d.checkNote(t, edited(t, part1End, part1End+"- ✅ [IaC#7](https://github.com/acme/IaC/issues/7): grant the deploy role — granted\n"))
}
