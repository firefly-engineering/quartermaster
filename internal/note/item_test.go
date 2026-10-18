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
