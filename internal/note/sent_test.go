package note

import (
	"strings"
	"testing"
)

// sentNote holds items in *Sent to other repos*, in both forms, and one elsewhere.
const sentNote = "# Worklog — 2026-10-18\n\n## ⏳ o/r\n\n### ⏳ What remains\n\n#### ⏳ Agent queue\n\n" +
	"- ⏳ not sent → infra, **sent** ^queued\n\n" +
	"#### ⏳ Sent to other repos\n\n" +
	"##### ⬜ open the port → infra, **to send**\n\nBody. ^port\n\n" +
	"- ⏳ [infra#40](https://github.com/o/infra/issues/40): rotate · 5 min → infra, **acked**, asked 10:00, https://github.com/o/infra/pull/1 — waits ^key\n" +
	"- ⏳ no status → infra ^bare\n" +
	"- ⏳ loose → infra (ops), **sent** ^loose\n"

func TestParseSent(t *testing.T) {
	t.Parallel()
	n, err := Parse([]byte(sentNote))
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]*Sent{
		"queued": nil, // not in Sent to other repos
		"port":   {Title: "open the port", To: "infra", Status: SentToSend},
		"key": {Title: "[infra#40](https://github.com/o/infra/issues/40): rotate · 5 min", To: "infra", Status: SentAcked,
			Note: "asked 10:00, https://github.com/o/infra/pull/1"},
		"bare":  nil,
		"loose": nil, // not the form: the label is one word
	} {
		got := n.Item(id).Sent
		if (got == nil) != (want == nil) || got != nil && *got != *want {
			t.Errorf("^%s: sent %+v, want %+v", id, got, want)
		}
	}
}

// TestRenderSent writes a sent status after the title, cost and route, and reads it back.
func TestRenderSent(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		u    ItemUnit
		want string
	}{
		{ItemUnit{ID: "a", Icon: IconWaiting, Title: "open the port", Sent: &SentUnit{To: "IaC", Status: SentSent}, Summary: "asked"},
			"- ⏳ open the port → IaC, **sent** — asked ^a\n"},
		{ItemUnit{ID: "b", Icon: IconTodo, Title: "x", Cost: "5 min", Sent: &SentUnit{To: "IaC", Status: SentAcked, Note: "https://github.com/o/r/pull/1"}},
			"- ⬜ x · 5 min → IaC, **acked**, https://github.com/o/r/pull/1 ^b\n"},
	} {
		got, err := Renderer{}.Item(c.u)
		if err != nil || string(got) != c.want {
			t.Errorf("%+v: %q %v, want %q", c.u, got, err, c.want)
		}
		head, _ := SplitSummary(strings.TrimSuffix(strings.TrimPrefix(c.want, "- "+string(c.u.Icon)+" "), " ^"+c.u.ID+"\n"))
		s := parseSent(head)
		if s == nil || s.To != c.u.Sent.To || s.Status != c.u.Sent.Status || s.Note != c.u.Sent.Note {
			t.Errorf("%q reads back as %+v", head, s)
		}
	}
	for _, u := range []ItemUnit{
		{ID: "a", Icon: IconTodo, Title: "x", Sent: &SentUnit{To: "IaC", Status: "posted"}},
		{ID: "a", Icon: IconTodo, Title: "x", Sent: &SentUnit{To: "two words", Status: SentSent}},
		{ID: "a", Icon: IconTodo, Title: "x", Sent: &SentUnit{Status: SentSent}},
		{ID: "a", Icon: IconTodo, Title: "x → IaC, **sent**", Sent: &SentUnit{To: "IaC", Status: SentSent}},
		{ID: "a", Icon: IconTodo, Title: "x", Sent: &SentUnit{To: "IaC", Status: SentSent, Note: "a — b"}},
	} {
		if _, err := (Renderer{}).Item(u); err == nil {
			t.Errorf("%+v %+v: no error", u, *u.Sent)
		}
	}
}

func TestRenderPart(t *testing.T) {
	t.Parallel()
	got, err := Renderer{}.Part(PartUnit{Icon: IconDone, N: 2, Kind: "Land", Title: "stack merged", Body: "- merged\n\n| a |\n|---|\n| b |\n\n"})
	if want := "### ✅ Part 2 — Land: stack merged\n\n- merged\n\n| a |\n|---|\n| b |\n"; err != nil || string(got) != want {
		t.Errorf("%q %v, want %q", got, err, want)
	}
	got, err = Renderer{}.Part(PartUnit{Icon: IconInProgress, N: 1, Kind: "Agent batch"})
	if want := "### 🔄 Part 1 — Agent batch\n"; err != nil || string(got) != want {
		t.Errorf("%q %v, want %q", got, err, want)
	}
	for _, u := range []PartUnit{{Icon: IconDone, N: 0, Kind: "Land"}, {Icon: IconDone, N: 1, Kind: "Meeting"}, {Icon: "x", N: 1, Kind: "Land"}, {Icon: IconDone, N: 1, Kind: "Land", Title: "a\nb"}} {
		if _, err := (Renderer{}).Part(u); err == nil {
			t.Errorf("%+v: no error", u)
		}
	}
}

func TestActionAndProcessLines(t *testing.T) {
	t.Parallel()
	r := Renderer{}
	if got, err := r.ActionLine(ActionUnit{ID: "grant", Text: " Grant the role "}); err != nil || string(got) != "- [ ] Grant the role ^grant\n" {
		t.Errorf("%q %v", got, err)
	}
	if got, err := r.ActionLine(ActionUnit{ID: "grant", Text: "Grant", Checked: true}); err != nil || string(got) != "- [x] Grant ^grant\n" {
		t.Errorf("%q %v", got, err)
	}
	for _, u := range []ActionUnit{{ID: "Bad", Text: "x"}, {ID: "a", Text: " "}, {ID: "a", Text: "a\nb"}, {ID: "a", Text: "✅ done"}, {ID: "a", Text: "x ^b"}} {
		if _, err := r.ActionLine(u); err == nil {
			t.Errorf("%+v: no error", u)
		}
	}
	if got, err := r.ProcessLine(" Ship smaller PRs "); err != nil || string(got) != "- Ship smaller PRs\n" {
		t.Errorf("%q %v", got, err)
	}
	for _, s := range []string{"", "—", "[ ] a box", "a\nb", "x ^id"} {
		if _, err := r.ProcessLine(s); err == nil {
			t.Errorf("%q: no error", s)
		}
	}
}

// TestRewordAction rewrites a box's text, keeping its indent, tick and nested lines.
func TestRewordAction(t *testing.T) {
	t.Parallel()
	src := "# Worklog — 2026-10-18\n\n## Process\n\n- x\n  - [x] Old text ^act\n    - a step\n"
	n, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got, err := RewordAction([]byte(src), *n.Action("act"), "New text")
	if want := strings.Replace(src, "Old text", "New text", 1); err != nil || string(got) != want {
		t.Errorf("%q %v, want %q", got, err, want)
	}
}

// TestPartLists records a Part's lists, and a box's first block.
func TestPartLists(t *testing.T) {
	t.Parallel()
	src := "# Worklog — 2026-10-18\n\n## ✅ o/r\n\n### ✅ Part 1 — Land: done\n\nProse.\n\n- one\n- [ ] two ^two\n  - nested\n"
	n, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pt := n.Projects[0].Parts[0]
	if len(pt.Lists) != 1 || src[pt.Lists[0].Start:pt.Lists[0].End] != "- one\n- [ ] two ^two\n  - nested\n" {
		t.Errorf("lists %+v", pt.Lists)
	}
	a := n.Action("two")
	if src[a.Head.Start:a.Head.End] != "- [ ] two ^two\n" || a.End != len(src) {
		t.Errorf("action %+v", a)
	}
}
