package note

import (
	"strings"
	"testing"
)

// itemNote holds a work item in each form: a ##### one with its button, a list one written on
// two lines with a nested step, an indented one, and a bucket holding only `- —`.
const itemNote = "# Worklog — 2026-10-18\n" + `
## Objectives

| Objective | Done when | Progress |
|---|---|---|
| Ship it (o/r) | released | ⬜ |

## ⬜ o/r

### ⬜ What remains

#### ⬜ Decisions for you

##### ⬜ [#14](https://github.com/o/r/issues/14): which store?

Unblocks #15.
[▶ grill in r](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-18&_item=store) ^store

> [!prompt]- Prompt sent to r
> ` + "```herdr-prompt id=store to=r\n> /grill-with-docs\n> ```\n" + `
- ⬜ [#16](https://github.com/o/r/issues/16): drop IE11,
  or keep it? ^ie11
  - a step
- plain
  - 🔄 nested — PR up ^nested

#### — Agent queue

- —
`

func TestItemHead(t *testing.T) {
	t.Parallel()
	src := []byte(itemNote)
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	checkSpans(t, src, n)
	for _, w := range []struct {
		id, head, icon, title, button string
	}{
		{"store", "##### ⬜ [#14](https://github.com/o/r/issues/14): which store?\n", "⬜",
			"[#14](https://github.com/o/r/issues/14): which store?", "grill in r"},
		{"ie11", "- ⬜ [#16](https://github.com/o/r/issues/16): drop IE11,\n  or keep it? ^ie11\n", "⬜",
			"[#16](https://github.com/o/r/issues/16): drop IE11, or keep it?", ""},
		{"nested", "  - 🔄 nested — PR up ^nested\n", "🔄", "nested — PR up", ""},
	} {
		it := n.Item(w.id)
		if it == nil {
			t.Fatalf("no item ^%s", w.id)
		}
		if at(src, it.Head) != w.head || at(src, it.IconSpan) != w.icon || it.Title != w.title || it.Button != w.button {
			t.Errorf("^%s: head %q icon %q title %q button %q", w.id, at(src, it.Head), at(src, it.IconSpan), it.Title, it.Button)
		}
	}
	if b := n.Projects[0].Remains.Buckets[1]; len(b.Dashes) != 1 || at(src, b.Dashes[0]) != "- —\n" {
		t.Errorf("dashes: %+v", b.Dashes)
	}
	decisions := n.Projects[0].Remains.Buckets[0]
	if len(decisions.Dashes) != 0 {
		t.Error("a bucket with items has a dash")
	}
	if l := decisions.Lists; len(l) != 1 || l[0].End != decisions.End || !strings.HasPrefix(at(src, l[0]), "- ⬜ [#16]") {
		t.Errorf("lists: %+v", l)
	}
	if tb := n.Objectives.Table; tb == nil ||
		at(src, *tb) != "| Objective | Done when | Progress |\n|---|---|---|\n| Ship it (o/r) | released | ⬜ |\n" {
		t.Errorf("objectives table: %+v", tb)
	}
}

// TestItemFields: the parser reads back the ref, cost and route the renderer writes, in both
// forms, and "" for an item without them (#79).
func TestItemFields(t *testing.T) {
	t.Parallel()
	d := &Dispatch{Pane: "w1:p3", Workspace: "/ws/o/r/issue-14"}
	items := []ItemUnit{
		{ID: "store", Icon: IconInProgress, Heading: true, Repo: "o/r", Ref: Ref{Repo: "o/r", N: 14}, Title: "which store?",
			Body: "Unblocks #15.", Cost: "10 min", Route: "/grill-with-docs", Summary: "grilling", Dispatch: d,
			Button: "grill in r", Prompt: &PromptUnit{To: "r", Body: "/grill-with-docs"}},
		{ID: "route-only", Icon: IconTodo, Heading: true, Title: "X", Route: "/triage"},
		{ID: "cost-only", Icon: IconTodo, Heading: true, Title: "Y", Body: "Two lines\nof prose.", Cost: "1.5 h"},
		{ID: "prose", Icon: IconTodo, Heading: true, Title: "Z", Body: "Done in 5 min."},
		{ID: "login", Icon: IconInReview, Repo: "o/r", Ref: Ref{Repo: "acme/IaC", N: 7}, Title: "login form",
			Cost: "20 min", Route: "/implement", Summary: "PR up", Dispatch: d},
		{ID: "cost-line", Icon: IconTodo, Title: "a, b", Cost: "5 min"},
		{ID: "route-line", Icon: IconTodo, Title: "c", Route: "/implement"},
		{ID: "bare", Icon: IconTodo, Title: "plain"},
	}
	sent := []ItemUnit{{ID: "grant", Icon: IconTodo, Repo: "o/r", Ref: Ref{Repo: "acme/IaC", N: 8}, Title: "grant",
		Cost: "5 min", Route: "/implement", Sent: &SentUnit{To: "IaC", Status: SentAcked, Note: "[#9](u)"}}}
	src, err := testRenderer.Note(Day{Date: "2026-10-18", Status: StatusUnit{Time: "09:30", Text: "open."},
		Projects: []ProjectUnit{{Icon: IconTodo, Repo: "o/r", Remains: IconTodo, Buckets: []BucketUnit{
			{Icon: IconTodo, Name: "Decisions for you", Items: items},
			{Icon: IconTodo, Name: SentBucket, Items: sent},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	for _, w := range []struct{ id, ref, cost, route string }{
		{"store", "o/r#14", "10 min", "/grill-with-docs"},
		{"route-only", "", "", "/triage"},
		{"cost-only", "", "1.5 h", ""},
		{"prose", "", "", ""},
		{"login", "acme/IaC#7", "20 min", "/implement"},
		{"cost-line", "", "5 min", ""},
		{"route-line", "", "", "/implement"},
		{"bare", "", "", ""},
		{"grant", "acme/IaC#8", "5 min", "/implement"},
	} {
		it := n.Item(w.id)
		if it == nil {
			t.Fatalf("no item ^%s in\n%s", w.id, src)
		}
		if it.Ref != w.ref || it.Cost != w.cost || it.Route != w.route {
			t.Errorf("^%s: ref %q cost %q route %q, want %q %q %q", w.id, it.Ref, it.Cost, it.Route, w.ref, w.cost, w.route)
		}
		// Its title unit renders its title line back, as a transition writes it.
		line, err := testRenderer.ItemTitle(it.TitleUnit())
		if head := string(src[it.IconSpan.Start:it.Head.End]); err != nil || string(line) != head {
			t.Errorf("^%s: title unit renders %q %v, want %q", w.id, line, err, head)
		}
	}
}

// TestItemFieldsOldForm: a ##### item whose body ends with a bare `<cost>.`, as written before
// the `Cost:` marker, reads as having no cost; its route still reads.
func TestItemFieldsOldForm(t *testing.T) {
	t.Parallel()
	src := []byte(strings.Replace(itemNote, "Unblocks #15.\n", "Unblocks #15. 10 min. Route: `/grill-with-docs`.\n", 1))
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if it := n.Item("store"); it.Ref != "o/r#14" || it.Cost != "" || it.Route != "/grill-with-docs" {
		t.Errorf("ref %q cost %q route %q", it.Ref, it.Cost, it.Route)
	}
}
