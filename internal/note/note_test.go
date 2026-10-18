package note

import (
	"bytes"
	"errors"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/firefly-engineering/quartermaster/skills"
)

// TestRealNote parses a day's note as the worklog skill wrote it.
func TestRealNote(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("testdata", "2026-10-18.md"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checkSpans(t, src, n)

	if n.FrontMatter == nil || n.FrontMatter.Start != 0 || !strings.HasSuffix(at(src, *n.FrontMatter), "\n---\n") {
		t.Fatalf("front matter: %+v", n.FrontMatter)
	}
	if n.Title.Date != "2026-10-18" || !strings.HasPrefix(at(src, n.Title.Span), "# Worklog — ") {
		t.Errorf("title: %+v", n.Title)
	}
	if n.Status == nil || n.Status.Time != "12:21" || n.Status.Label != "unblocked" {
		t.Fatalf("status: %+v", n.Status)
	}
	if !strings.HasPrefix(at(src, n.Status.Span), "**12:21 UTC, unblocked:**") {
		t.Errorf("status span: %q", at(src, n.Status.Span))
	}

	if n.Objectives == nil || len(n.Objectives.Rows) != 1 {
		t.Fatalf("objectives: %+v", n.Objectives)
	}
	o := n.Objectives.Rows[0]
	if o.Icon != IconInProgress || o.Objective != "A working version of quartermaster (quartermaster)" ||
		o.Progress != "🔄 3/16 closed" || !strings.HasPrefix(at(src, o.Span), "| A working version") {
		t.Errorf("objective row: %+v", o)
	}

	if n.Next == nil || len(n.Next.Items) != 1 {
		t.Fatalf("next: %+v", n.Next)
	}
	if it := n.Next.Items[0]; it.Ref != "qm-wave1" || it.Box != NoBox || !strings.HasPrefix(at(src, it.Span), "1. Start wave 1") {
		t.Errorf("next item: %+v", it)
	}

	if len(n.Projects) != 1 {
		t.Fatalf("projects: %d", len(n.Projects))
	}
	p := n.Projects[0]
	if p.Repo != "firefly-engineering/quartermaster" || p.Icon != IconInProgress {
		t.Errorf("project: %+v", p)
	}
	if !strings.HasPrefix(at(src, p.Span), "## 🔄 firefly-engineering/quartermaster\n") ||
		!strings.HasSuffix(at(src, p.Span), "**done**: go-toolchain 7 adopted in [`ff9549bf`](https://github.com/firefly-engineering/quartermaster/commit/ff9549bf4fb7).\n\n#### — Outside input: chase\n\n- —\n\n#### — Waiting\n\n- —\n") {
		t.Errorf("project span: %q", at(src, p.Span))
	}
	wantParts := []struct {
		icon  Icon
		kind  string
		title string
	}{
		{IconDone, "Human session", "spec, scaffold and tickets, done"},
		{IconDone, "Human session", "three open questions, settled"},
		{IconInProgress, "Agent batch", "wave 1, **running**"},
	}
	if len(p.Parts) != len(wantParts) {
		t.Fatalf("parts: %d", len(p.Parts))
	}
	for i, w := range wantParts {
		got := p.Parts[i]
		if got.N != i+1 || got.Icon != w.icon || got.Kind != w.kind || got.Title != w.title {
			t.Errorf("part %d: %+v", i+1, got)
		}
	}

	r := p.Remains
	if r == nil || r.Icon != IconInProgress {
		t.Fatalf("what remains: %+v", r)
	}
	wantBuckets := []struct {
		name string
		icon Icon
	}{
		{"Decisions for you", IconNone},
		{"Your actions", IconNone},
		{"Agent queue", IconTodo},
		{"Sent to other repos", IconNone},
		{"Outside input: chase", IconNone},
		{"Waiting", IconNone},
	}
	if len(r.Buckets) != len(wantBuckets) {
		t.Fatalf("buckets: %d", len(r.Buckets))
	}
	for i, w := range wantBuckets {
		if b := r.Buckets[i]; b.Name != w.name || b.Icon != w.icon || !strings.HasPrefix(at(src, b.Span), "#### ") {
			t.Errorf("bucket %d: %+v", i, b)
		}
	}

	q := r.Buckets[2]
	if len(q.Items) != 1 {
		t.Fatalf("agent queue items: %d", len(q.Items))
	}
	it := q.Items[0]
	if it.ID != "qm-wave1" || it.Icon != IconTodo || !it.Heading || it.Title != "Wave 1: #1, #2, #7" {
		t.Errorf("item: %+v", it)
	}
	if n.Item("qm-wave1") != it {
		t.Error("Item lookup")
	}
	body := at(src, it.Span)
	if !strings.HasPrefix(body, "##### ⬜ Wave 1") || !strings.HasSuffix(body, "needs its first real vendorHash.\n> ```\n") {
		t.Errorf("item span: %q", body)
	}
	pr := it.Prompt
	if pr == nil || pr.ID != "qm-wave1" || pr.To != "quartermaster" ||
		!strings.HasPrefix(pr.Body, "/mass-implement firefly-engineering/quartermaster #1 #2 #7 — ") ||
		!strings.HasSuffix(pr.Body, "vendorHash.\n") || strings.Contains(pr.Body, "> ") {
		t.Fatalf("prompt: %+v", pr)
	}
	if !strings.HasPrefix(at(src, pr.Span), "> [!prompt]- Prompt sent to quartermaster\n") {
		t.Errorf("prompt span: %q", at(src, pr.Span))
	}

	if n.CarryOver == nil || len(n.CarryOver.Rows) != 7 {
		t.Fatalf("carry-over: %+v", n.CarryOver)
	}
	if c := n.CarryOver.Rows[0]; c.Item != "Bring the INTEGRA-7 online (sigma/integral)" || c.State != "offline" ||
		c.Verdict != "defer: Sunday, no device work" {
		t.Errorf("carry-over row: %+v", c)
	}
	if n.Process == nil || n.Process.List == nil || at(src, *n.Process.List) != "- —\n" {
		t.Errorf("process: %+v", n.Process)
	}
	if len(n.Actions) != 0 || len(n.Anchors) != 0 {
		t.Errorf("actions %v, anchors %v", n.Actions, n.Anchors)
	}
}

// TestTemplate parses the skill's own template with its placeholders filled in, so a template
// change the parser cannot read fails here.
func TestTemplate(t *testing.T) {
	t.Parallel()
	src := filledTemplate(t)
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, src)
	}
	checkSpans(t, src, n)

	if n.FrontMatter == nil || n.Title.Date != "2026-10-18" || n.Status == nil || n.Status.Time != "09:30" || n.Status.Label != "" {
		t.Fatalf("head: %+v %+v %+v", n.FrontMatter, n.Title, n.Status)
	}
	if len(n.Objectives.Rows) != 1 || n.Objectives.Rows[0].Icon != IconTodo {
		t.Errorf("objectives: %+v", n.Objectives.Rows)
	}
	if len(n.Next.Items) != 2 || n.Next.Items[0].Ref != "grill-2" || n.Next.Items[1].Ref != "grill-2" {
		t.Errorf("next: %+v", n.Next.Items)
	}
	// A Next line's button span runs from the field separator before it to its link's end.
	if b := at(src, n.Next.Items[0].Button); !strings.HasPrefix(b, " · [▶ ") || !strings.HasSuffix(b, "&_item=grill-2)") ||
		!strings.HasSuffix(strings.TrimSuffix(at(src, n.Next.Items[0].Span), "\n"), b) {
		t.Errorf("next button: %q", b)
	}
	if b := n.Next.Items[1].Button; b != (Span{}) {
		t.Errorf("next line without a button: button %+v", b)
	}
	if len(n.Projects) != 1 {
		t.Fatalf("projects: %d", len(n.Projects))
	}
	p := n.Projects[0]
	if p.Repo != "firefly-engineering/quartermaster" || len(p.Parts) != 1 || p.Parts[0].Kind != "Human session" ||
		p.Parts[0].Title != "spec, done" {
		t.Errorf("project: %+v, parts %+v", p, p.Parts)
	}
	var names []string
	for _, b := range p.Remains.Buckets {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != strings.Join(BucketNames, ",") {
		t.Errorf("buckets: %v", names)
	}
	it := n.Item("grill-2")
	if it == nil || !it.Heading || it.Prompt == nil || it.Prompt.To != "quartermaster" ||
		it.Prompt.Body != "/grill-with-docs https://github.com/firefly-engineering/quartermaster/issues/2\n" {
		t.Fatalf("item: %+v", it)
	}
	if p.Remains.Buckets[0].Items[0] != it {
		t.Error("item not in Decisions for you")
	}
	if len(n.CarryOver.Rows) != 1 || n.CarryOver.Rows[0].Verdict != "defer: tomorrow" {
		t.Errorf("carry-over: %+v", n.CarryOver.Rows)
	}
}

// TestCarryOverRowRef reads the block id a carry-over row's item links, from its own text only.
func TestCarryOverRowRef(t *testing.T) {
	t.Parallel()
	for item, want := range map[string]string{
		`Vendor credential [[2026-10-16#^vendor-cred\|^vendor-cred]]`: "vendor-cred",
		"Vendor credential [[2026-10-16#^vendor-cred]]":               "vendor-cred",
		"[[2026-10-16]] then [[2026-10-16#^b]]":                       "b",
		"Vendor credential `[[2026-10-16#^vendor-cred]]`":             "",
		"[[2026-10-16#^Bad id]]":                                      "",
		"[#9](https://github.com/acme/web/issues/9) flaky test":       "",
	} {
		src := "# Worklog — 2026-10-19\n\n**08:00 UTC:** ok.\n\n## Carry-over\n\n" +
			"| Item | State now | Verdict |\n|---|---|---|\n| " + item + " | open | |\n"
		n, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", item, err)
		}
		if got := n.CarryOver.Rows[0].Ref; got != want {
			t.Errorf("%s: ref %q, want %q", item, got, want)
		}
		wantNote := ""
		if want != "" {
			wantNote = "2026-10-16"
		}
		if got := n.CarryOver.Rows[0].RefNote; got != wantNote {
			t.Errorf("%s: ref note %q, want %q", item, got, wantNote)
		}
		if n.CarryOver.Table == nil || n.CarryOver.Table.End != len(src) {
			t.Errorf("%s: table %+v", item, n.CarryOver.Table)
		}
	}
}

// TestUnits covers the units the two notes above do not hold.
func TestUnits(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("testdata", "units.md"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	checkSpans(t, src, n)

	if n.FrontMatter != nil {
		t.Errorf("front matter: %+v", n.FrontMatter)
	}
	if got := n.Next.Items; len(got) != 3 || got[0].Box != Unchecked || got[0].Ref != "rotate" ||
		got[1].Box != Checked || got[2].Box != NoBox {
		t.Errorf("next: %+v", got)
	}

	want := []struct {
		id      string
		checked bool
		text    string
	}{
		{"", false, "Rotate the token · 5 min, manual · [[#^rotate|details]]"},
		{"", true, "Tick the box · done"},
		{"", true, "Review the diff"},
		{"rotate", false, "Rotate the token"},
		{"", false, "Nested, and ordered"},
		{"", false, "Ask about the budget"},
	}
	if len(n.Actions) != len(want) {
		t.Fatalf("actions: %+v", n.Actions)
	}
	for i, w := range want {
		a := n.Actions[i]
		if a.ID != w.id || a.Checked != w.checked || a.Text != w.text {
			t.Errorf("action %d: %+v", i, a)
		}
	}
	if a := n.Actions[3]; at(src, a.Span) != "- [ ] Rotate the token ^rotate\n  1. [ ] Nested, and ordered\n" {
		t.Errorf("action span: %q", at(src, a.Span))
	}

	p := n.Projects[0]
	if got := p.Parts[0].Items; len(got) != 1 || got[0].ID != "land-3" || got[0].Icon != IconDone || got[0].Heading ||
		got[0].Title != "[#3](https://example.com/3) merged" {
		t.Errorf("part items: %+v", got)
	}
	if p.Parts[0].Kind != "Land" || p.Parts[0].Title != "done" {
		t.Errorf("part: %+v", p.Parts[0])
	}
	q := p.Remains.Buckets[0]
	if len(q.Items) != 3 {
		t.Fatalf("queue items: %+v", q.Items)
	}
	if it := q.Items[0]; it.ID != "send-iac" || it.Icon != IconWaiting || !it.Heading || it.Prompt != nil {
		t.Errorf("heading item without prompt: %+v", it)
	}
	if it := q.Items[1]; it.ID != "impl-4" || it.Icon != IconInReview || it.Heading ||
		at(src, it.Span) != "- 🔍 [#4](https://example.com/4) in review ^impl-4\n  - a note on it\n  - ⬜ a step of the item, not an entry\n" {
		t.Errorf("list item: %+v %q", it, at(src, it.Span))
	}
	if it := q.Items[2]; it.ID != "deep-1" || it.Icon != IconDropped {
		t.Errorf("nested list item: %+v", it)
	}
	// Entries: led by a state icon, with no id; not `—`, prose, a checkbox or a work item's step.
	if got := q.Entries; len(got) != 2 ||
		got[0].Icon != IconInProgress || got[0].Text != "a nested entry" || at(src, got[0].Span) != "  - 🔄 a nested entry\n" ||
		got[1].Icon != IconTodo || got[1].Text != "[#5](https://example.com/5) an entry with no id, on two lines" ||
		at(src, got[1].Span) != "- ⬜ [#5](https://example.com/5) an entry with no id,\n  on two lines\n" {
		t.Errorf("queue entries: %+v", got)
	}
	if got := p.Remains.Buckets[1].Entries; len(got) != 0 {
		t.Errorf("actions bucket entries: %+v", got)
	}
	if got := n.Process.Entries; len(got) != 1 || got[0].Text != "File a ticket for the cache" || got[0].Icon != "" ||
		at(src, got[0].Span) != "- File a ticket for the cache\n" {
		t.Errorf("process entries: %+v", got)
	}
	if len(n.Anchors) != 2 || n.Anchors[0].ID != "part-1" || n.Anchors[1].ID != "queue" {
		t.Errorf("anchors: %+v", n.Anchors)
	}
	if n.Item("part-1") != nil || n.Item("nope") != nil {
		t.Error("Item resolved a non-item")
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	const head = "# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n"
	const proj = head + "## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n"
	cases := []struct {
		name string
		src  string
		line int
		msg  string
	}{
		{"unclosed front matter", "---\ndate: x\n# Worklog — 2026-10-18\n", 1, "front matter"},
		{"no title", "## Next\n", 1, "content before"},
		{"bad date", "# Worklog — 18/10\n", 1, "date"},
		{"bad title", "# Diary\n", 1, "Worklog"},
		{"two titles", head + "# Worklog — 2026-10-18\n", 5, "second"},
		{"bad status", "# Worklog — 2026-10-18\n\n**9h UTC:** open.\n", 3, "status"},
		{"unknown section", head + "## Log\n", 5, "section"},
		{"repeated section", head + "## Next\n\n## Next\n", 7, "repeated"},
		{"bad repo", head + "## ⬜ not a repo\n", 5, "section"},
		{"bad icon", head + "## 🙂 o/r\n", 5, "icon"},
		{"empty heading", head + "##\n", 5, "section"},
		{"h3 outside project", head + "## Next\n\n### ⬜ What remains\n", 7, "project"},
		{"bad part", head + "## ⬜ o/r\n\n### ⬜ Part one — Land: x\n", 7, "Part"},
		{"bad part kind", head + "## ⬜ o/r\n\n### ⬜ Part 1 — Nap: x\n", 7, "kind"},
		{"bucket outside remains", head + "## ⬜ o/r\n\n#### ⬜ Waiting\n", 7, "What remains"},
		{"unknown bucket", head + "## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Someday\n", 9, "bucket"},
		{"repeated bucket", proj + "#### ⬜ Agent queue\n", 11, "repeated"},
		{"h5 outside bucket", head + "## ⬜ o/r\n\n##### ⬜ x\n", 7, "bucket"},
		{"h6", proj + "###### x\n", 11, "level 6"},
		{"heading item without id", proj + "##### ⬜ x\n\nno id here.\n", 11, "^<item-id>"},
		{"heading item at end", proj + "##### ⬜ x\n", 11, "^<item-id>"},
		{"heading item with checkbox icon", proj + "##### [ ] x\n\ny ^a\n", 11, "icon"},
		{"duplicate id", proj + "##### ⬜ x\n\ny ^a\n\n- ⬜ z ^a\n", 15, "duplicate"},
		{"invalid id", proj + "- ⬜ z ^Bad\n", 11, "item id"},
		{"list id without icon", proj + "- z ^a\n", 11, "icon"},
		{"checkbox and icon", proj + "- [ ] ⬜ z\n", 11, "checkbox"},
		{"id outside project", head + "## Objectives\n\nx ^a\n", 7, "placed"},
		{"id in remains before bucket", head + "## ⬜ o/r\n\n### ⬜ What remains\n\n- ⬜ x ^a\n", 9, "placed"},
		{"stray herdr-prompt", proj + "```herdr-prompt id=a to=b\nx\n```\n", 11, "herdr-prompt"},
		{"prompt not after item", proj + "> [!prompt]- p\n> ```herdr-prompt id=a to=b\n> x\n> ```\n", 11, "work item"},
		{"prompt id mismatch", proj + "##### ⬜ x\n\ny ^a\n\n> [!prompt]- p\n> ```herdr-prompt id=b to=t\n> x\n> ```\n", 16, "id"},
		{"prompt without fence", proj + "##### ⬜ x\n\ny ^a\n\n> [!prompt]- p\n> x\n", 15, "herdr-prompt"},
		{"prompt without to", proj + "##### ⬜ x\n\ny ^a\n\n> [!prompt]- p\n> ```herdr-prompt id=a\n> x\n> ```\n", 16, "to="},
		{"prompt unknown key", proj + "##### ⬜ x\n\ny ^a\n\n> [!prompt]- p\n> ```herdr-prompt id=a to=b x=y\n> z\n> ```\n", 16, "x=y"},
		{"unordered next", head + "## Next\n\n- x\n", 7, "ordered"},
		{"two next lists", head + "## Next\n\n1. x\n\ntext\n\n1. y\n", 11, "one list"},
		{"objectives header", head + "## Objectives\n\n| A | B |\n|---|---|\n| x | y |\n", 7, "header"},
		{"objective without icon", head + "## Objectives\n\n| Objective | Done when | Progress |\n|---|---|---|\n| x | y | 1/2 |\n", 9, "icon"},
		{"two objectives tables", head + "## Objectives\n\n| Objective | Done when | Progress |\n|---|---|---|\n\n| Objective | Done when | Progress |\n|---|---|---|\n", 10, "one table"},
		{"carry-over header", head + "## Carry-over\n\n| A | B | C |\n|---|---|---|\n", 7, "header"},
		{"two process lists", head + "## Process\n\n- x\n\ntext\n\n- y\n", 11, "one list"},
		{"bad next ref", head + "## Next\n\n1. go · [[#^Bad|details]]\n", 7, "item id"},
		{"dangling next ref", head + "## Next\n\n1. go · [[#^gone|details]]\n", 7, "^gone"},
		{"expanded prompt callout", proj + "##### ⬜ x\n\ny ^a\n\n> [!prompt] p\n> ```herdr-prompt id=a to=b\n> x\n> ```\n", 15, "[!prompt]-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(c.src))
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("want a ParseError, got %v", err)
			}
			if pe.Line != c.line || !strings.Contains(pe.Msg, c.msg) {
				t.Errorf("got %q, want line %d containing %q", err, c.line, c.msg)
			}
			if !strings.HasPrefix(err.Error(), "line "+strconv.Itoa(c.line)+": ") {
				t.Errorf("error text %q does not lead with its line", err)
			}
		})
	}
}

// TestParseIntermediate: a dangling *Next* details link parses halfway through an edit, and
// every other error still stops it.
func TestParseIntermediate(t *testing.T) {
	t.Parallel()
	const head = "# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n"
	n, err := ParseIntermediate([]byte(head + "## Next\n\n1. go · [[#^gone|details]]\n"))
	if err != nil || n.Next == nil || len(n.Next.Items) != 1 || n.Next.Items[0].Ref != "gone" {
		t.Fatalf("got %+v, %v", n, err)
	}
	if _, err := ParseIntermediate([]byte(head + "## Next\n\n1. go · [[#^Bad|details]]\n")); err == nil {
		t.Error("an invalid details link parsed")
	}
}

// TestPartialResult: a note with an error still returns every unit that could be placed.
func TestPartialResult(t *testing.T) {
	t.Parallel()
	src := "# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n## Log\n\n## Next\n\n1. go\n"
	n, err := Parse([]byte(src))
	if err == nil || n == nil || n.Next == nil || len(n.Next.Items) != 1 {
		t.Fatalf("err %v, note %+v", err, n)
	}
}

// TestNextRefResolves: a *Next* link may point at a work item or an anchor (#21).
func TestNextRefResolves(t *testing.T) {
	t.Parallel()
	src := "# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n## Next\n\n1. a · [[#^it|details]]\n2. b · [[#^part-1|details]]\n\n" +
		"## ⬜ o/r\n\n### ⬜ Part 1 — Land: x, done\n\nmerged. ^part-1\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n- ⬜ y ^it\n"
	if _, err := Parse([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

// TestNextButton: a *Next* line's button span takes one field separator, before it or after
// it, so cutting it leaves the line's other fields parted as they were (#59).
func TestNextButton(t *testing.T) {
	t.Parallel()
	const button = "[▶ go](obsidian://shell-commands/?vault=v&execute=c&_date=2026-10-18&_item=it)"
	for line, want := range map[string]string{
		"1. a · [[#^it|details]] · " + button + "\n":         "1. a · [[#^it|details]]",
		"1. a · " + button + " · [[#^it|details]]\n":         "1. a · [[#^it|details]]",
		"1. " + button + " · a · [[#^it|details]]\n":         "1. a · [[#^it|details]]",
		"1. a · [[#^it|details]] · [link](https://x.org/)\n": "1. a · [[#^it|details]] · [link](https://x.org/)",
	} {
		src := "# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n## Next\n\n" + line + "\n" +
			"## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n- ⬜ y ^it\n"
		n, err := Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		it := n.Next.Items[0]
		got := strings.TrimSuffix(string(Replace([]byte(src), it.Button, nil)[it.Start:it.End-(it.Button.End-it.Button.Start)]), "\n")
		if got != want {
			t.Errorf("%q: cut to %q, want %q", line, got, want)
		}
	}
}

// TestCRLFFrontMatter: front matter written with CRLF line ends is read, not refused (#21).
func TestCRLFFrontMatter(t *testing.T) {
	t.Parallel()
	src := "---\r\ndate: 2026-10-18\r\n---\r\n\n# Worklog — 2026-10-18\n"
	n, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if n.FrontMatter == nil || n.FrontMatter.End != len("---\r\ndate: 2026-10-18\r\n---\r\n") {
		t.Fatalf("front matter: %+v", n.FrontMatter)
	}
}

// TestNoRegexp keeps ADR 0002's rule mechanical: the parser imports no regexp package.
func TestNoRegexp(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v %v", files, err)
	}
	for _, f := range files {
		af, err := goparser.ParseFile(token.NewFileSet(), f, nil, goparser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "regexp" || strings.HasSuffix(p, "/regexp") {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}

// checkSpans holds for every unit: inside the source, on line boundaries at its start, and not
// ending on a blank line.
func checkSpans(t *testing.T, src []byte, n *Note) {
	t.Helper()
	var spans []Span
	add := func(s Span) { spans = append(spans, s) }
	if n.FrontMatter != nil {
		add(*n.FrontMatter)
	}
	add(n.Title.Span)
	if n.Status != nil {
		add(n.Status.Span)
	}
	if n.Objectives != nil {
		add(n.Objectives.Span)
		for _, r := range n.Objectives.Rows {
			add(r.Span)
		}
	}
	if n.Next != nil {
		add(n.Next.Span)
		for _, r := range n.Next.Items {
			add(r.Span)
		}
	}
	for _, p := range n.Projects {
		add(p.Span)
		add(p.Heading)
		for _, pt := range p.Parts {
			add(pt.Span)
			add(pt.Heading)
			for _, it := range pt.Items {
				add(it.Span)
			}
		}
		if p.Remains != nil {
			add(p.Remains.Span)
			for _, b := range p.Remains.Buckets {
				add(b.Span)
				add(b.Heading)
				for _, it := range b.Items {
					add(it.Span)
					if it.Prompt != nil {
						add(it.Prompt.Span)
					}
				}
				for _, e := range b.Entries {
					add(e.Span)
				}
			}
		}
	}
	if n.CarryOver != nil {
		add(n.CarryOver.Span)
		for _, r := range n.CarryOver.Rows {
			add(r.Span)
		}
	}
	if n.Process != nil {
		add(n.Process.Span)
		for _, e := range n.Process.Entries {
			add(e.Span)
		}
	}
	for _, a := range n.Actions {
		add(a.Span)
	}
	for _, a := range n.Anchors {
		add(a.Span)
	}
	for _, s := range spans {
		if s.Start < 0 || s.End > len(src) || s.Start >= s.End {
			t.Fatalf("span %+v out of source", s)
		}
		if s.Start > 0 && src[s.Start-1] != '\n' {
			t.Errorf("span %+v does not start a line: %q", s, at(src, s))
		}
		if want := bytes.Count(src[:s.Start], []byte("\n")) + 1; s.Line != want {
			t.Errorf("span %+v: line %d, want %d", s, s.Line, want)
		}
		if body := at(src, s); strings.HasSuffix(body, "\n\n") || strings.TrimSpace(body) == "" {
			t.Errorf("span %+v ends on a blank line: %q", s, body)
		}
	}
}

func at(src []byte, s Span) string { return string(src[s.Start:s.End]) }

// filledTemplate returns the markdown block of the skill's template.md with every placeholder
// filled. A placeholder missing from the table fails the test, so it tracks the template.
func filledTemplate(t *testing.T) []byte {
	t.Helper()
	tmpl, err := fs.ReadFile(skills.Worklog, "worklog/template.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := goldmark.New().Parser().Parse(text.NewReader(tmpl))
	var block []byte
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		if f, ok := c.(*ast.FencedCodeBlock); ok && string(f.Language(tmpl)) == "markdown" {
			for i := range f.Lines().Len() {
				seg := f.Lines().At(i)
				block = append(block, seg.Value(tmpl)...)
			}
		}
	}
	if block == nil {
		t.Fatal("template.md has no markdown block")
	}
	fill := map[string]string{
		"YYYY-MM-DD":                     "2026-10-18",
		"previous date":                  "2026-10-17",
		"owner/repo":                     "firefly-engineering/quartermaster",
		"HH:MM":                          "09:30",
		"one line: where the day stands": "opened.",
		"objective":                      "Ship the parser",
		"repo":                           "quartermaster",
		"checkable condition":            "#2 closed",
		"icon":                           "⬜",
		"2/3 decisions · stack in review · missed: reason": "0/1",
		"action":                                 "Settle #2",
		"cost":                                   "20 min",
		"item-id":                                "grill-2",
		"verb":                                   "grill",
		"workspace":                              "quartermaster",
		"id":                                     "nf8lgvkx5s",
		"date":                                   "2026-10-18",
		"manual action, no session can start it": "Rotate the token",
		"Human session | Agent batch | Land":     "Human session",
		"title":                                  "spec",
		"running / done":                         "done",
		"one line per decision with its link · or a Ticket | PR | Outcome table · or the merge and its post-merge ops": "- ✅ decided.",
		"question":        "is the parser strict?",
		"…":               "#3",
		"skill":           "/grill-with-docs",
		"why that route":  "It touches ADR 0002",
		"workspace label": "quartermaster",
		"self-contained prompt, e.g. /grill-with-docs https://github.com/<o>/<r>/issues/N — the question and its context": "/grill-with-docs https://github.com/firefly-engineering/quartermaster/issues/2",
		"a decision with no prompt yet, one line": "a decision with no prompt yet",
		"item":                          "a grant",
		"to send / sent / acked / done": "sent",
		"links":                         "[IaC#1](https://example.com/1)",
		"item with link":                "[#9](https://example.com/9)",
		"refreshed state":               "open",
		"keep / defer / drop: reason":   "defer: tomorrow",
	}
	var out []byte
	for len(block) > 0 {
		i := bytes.IndexByte(block, '<')
		if i < 0 {
			out = append(out, block...)
			break
		}
		out = append(out, block[:i]...)
		depth, j := 0, i
		for ; j < len(block); j++ {
			if block[j] == '<' {
				depth++
			} else if block[j] == '>' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if j == len(block) {
			t.Fatalf("unclosed placeholder at %q", block[i:])
		}
		name := string(block[i+1 : j])
		v, ok := fill[name]
		if !ok {
			t.Fatalf("template placeholder <%s> has no fill value", name)
		}
		out = append(out, v...)
		block = block[j+1:]
	}
	return out
}
