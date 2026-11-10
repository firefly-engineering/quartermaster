package note

import (
	"strings"
	"testing"
)

// dispatchNote holds dispatched work items in each form, a list one and ##### ones (the marker
// after a button, or in prose), and an item never dispatched.
const dispatchNote = "# Worklog — 2026-10-18\n" + `
## 🔄 o/r

### 🔄 What remains

#### 🔄 Agent queue

- 🔄 [#11](https://github.com/o/r/issues/11): dispatch — started <!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 --> ^d11
- ⬜ [r#12](https://github.com/o/r/issues/12): refresh ^d12

##### 🔄 [r#13](https://github.com/o/r/issues/13): send

Port it.
[▶ send to r](obsidian://shell-commands/?vault=worklog&execute=abc&_date=2026-10-18&_item=d13) <!-- qm pane=w1:p4 ws=/ws/o/r/issue-13 --> ^d13

> [!prompt]- Prompt sent to r
> ` + "```herdr-prompt id=d13 to=r\n> /implement\n> ```\n" + `
##### ⬜ [r#15](https://github.com/o/r/issues/15): canary

^d15

> [!prompt]- Prompt sent to r
> ` + "```herdr-prompt id=d15 to=r\n> /implement\n> ```\n" + `
##### ⬜ [r#16](https://github.com/o/r/issues/16): bare

^d16

##### 🔄 [r#14](https://github.com/o/r/issues/14): which store?

Working on it. <!-- qm pane=w1:p5 ws=/ws/o/r/issue-14 --> ^d14
`

func TestParseDispatch(t *testing.T) {
	t.Parallel()
	src := []byte(dispatchNote)
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	checkSpans(t, src, n)
	for id, want := range map[string]*Dispatch{
		"d11": {Pane: "w1:p3", Workspace: "/ws/o/r/issue-11"},
		"d12": nil,
		"d13": {Pane: "w1:p4", Workspace: "/ws/o/r/issue-13"},
		"d14": {Pane: "w1:p5", Workspace: "/ws/o/r/issue-14"},
	} {
		it := n.Item(id)
		if it == nil {
			t.Fatalf("no item ^%s", id)
		}
		if (it.Dispatch == nil) != (want == nil) || want != nil && *it.Dispatch != *want {
			t.Errorf("^%s: dispatch %+v, want %+v", id, it.Dispatch, want)
		}
	}
	if got := n.Item("d11").Title; got != "[#11](https://github.com/o/r/issues/11): dispatch — started" {
		t.Errorf("d11 title %q: the marker is not part of it", got)
	}
	if p := n.Item("d13").Prompt; p == nil || p.To != "r" {
		t.Errorf("d13 prompt %+v", p)
	}
}

// TestParseDispatchIgnoresOtherComments reads no marker from another comment, a malformed `qm`
// one, or one in a code span, which is code, not HTML.
func TestParseDispatchIgnoresOtherComments(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		"<!-- a note -->", "<!-- qm -->", "<!-- qm pane= -->", "<!-- qm pane=p ws=/x extra -->",
		"<!-- qm pane=p pane=q ws=/x -->", "<!-- qmx pane=p ws=/x -->", "<!-- qm ws=/x ws=/y -->", "<!-- qm pane= ws=/x -->",
		"<!-- qm pane=p ws=/x tab=t -->", "`<!-- qm pane=p ws=/x -->`",
	} {
		src := []byte(strings.Replace(dispatchNote, "<!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 -->", c, 1))
		n, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if d := n.Item("d11").Dispatch; d != nil {
			t.Errorf("%s: dispatch %+v", c, d)
		}
	}
}

// TestDispatchMarker renders a marker and parses it back, with either part absent too: a
// finished dispatch whose tab refresh closed keeps only its ws.
func TestDispatchMarker(t *testing.T) {
	t.Parallel()
	for d, want := range map[Dispatch]string{
		{Pane: "w1:p3", Workspace: "/ws/o/r/issue-11"}: "<!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 -->",
		{Workspace: "/ws/o/r/issue-11"}:                "<!-- qm ws=/ws/o/r/issue-11 -->",
		{Pane: "w1:p3"}:                                "<!-- qm pane=w1:p3 -->",
	} {
		got := (Renderer{}).DispatchMarker(d)
		if got != want {
			t.Errorf("marker %q, want %q", got, want)
		}
		n, err := Parse([]byte(strings.Replace(dispatchNote, "<!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 -->", got, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if it := n.Item("d11"); it.Dispatch == nil || *it.Dispatch != d {
			t.Errorf("%s parses as %+v", got, it.Dispatch)
		}
	}
}

func TestSetDispatch(t *testing.T) {
	t.Parallel()
	src := []byte(dispatchNote)
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	d := Dispatch{Pane: "w2:p1", Workspace: "/ws/o/r/issue-12"}
	out, err := SetDispatch(n, src, "d12", d)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(dispatchNote, "refresh ^d12", "refresh <!-- qm pane=w2:p1 ws=/ws/o/r/issue-12 --> ^d12", 1)
	if string(out) != want {
		t.Errorf("list item:\n%s", out)
	}
	// A dispatched item's marker is replaced, in either form.
	n, _ = Parse(src)
	out, err = SetDispatch(n, src, "d13", d)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(dispatchNote, "pane=w1:p4 ws=/ws/o/r/issue-13", "pane=w2:p1 ws=/ws/o/r/issue-12", 1); string(out) != want {
		t.Errorf("##### item:\n%s", out)
	}
	// Its pane dropped, the marker keeps its ws.
	n, _ = Parse(src)
	out, err = SetDispatch(n, src, "d11", Dispatch{Workspace: "/ws/o/r/issue-11"})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(dispatchNote, "<!-- qm pane=w1:p3 ws=", "<!-- qm ws=", 1); string(out) != want {
		t.Errorf("pane dropped:\n%s", out)
	}
	// An empty dispatch removes the marker, with the space it was written with, in either form.
	for _, c := range []struct{ id, marker string }{
		{"d11", " <!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 -->"},
		{"d13", " <!-- qm pane=w1:p4 ws=/ws/o/r/issue-13 -->"},
	} {
		n, _ = Parse(src)
		out, err = SetDispatch(n, src, c.id, Dispatch{})
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(dispatchNote, c.marker, "", 1); string(out) != want {
			t.Errorf("%s marker removed:\n%s", c.id, out)
		}
	}
	// An item with no marker is left as it is.
	n, _ = Parse(src)
	if out, err = SetDispatch(n, src, "d12", Dispatch{}); err != nil || string(out) != dispatchNote {
		t.Errorf("no marker to remove: %v\n%s", err, out)
	}
	for _, bad := range []Dispatch{
		{Pane: "a b", Workspace: "/x"},
		{Pane: "p", Workspace: "/a b"},
		{Pane: "p", Workspace: "/a-->"},
		{Pane: "p", Workspace: "relative"},
	} {
		if _, err := SetDispatch(n, src, "d12", bad); err == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
	if _, err := SetDispatch(n, src, "nope", d); err == nil {
		t.Error("no item: no error")
	}
}

// TestSetDispatchOnBareID: a marker written on an item whose paragraph holds only ^<id>
// would start the paragraph, and CommonMark would read the line as an HTML block, not a
// paragraph: the item's ▶ label is written in italics before the marker, where its button
// would go, and the write is refused when there is no label to write (#168).
func TestSetDispatchOnBareID(t *testing.T) {
	t.Parallel()
	src := []byte(dispatchNote)
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	d := Dispatch{Pane: "w2:p1", Workspace: "/ws/o/r/issue-12"}
	out, err := SetDispatch(n, src, "d15", d)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(dispatchNote, "\n^d15\n",
		"\n*send to r* <!-- qm pane=w2:p1 ws=/ws/o/r/issue-12 --> ^d15\n", 1)
	if string(out) != want {
		t.Errorf("bare ^d15:\n%s", out)
	}
	if _, err = Parse(out); err != nil {
		t.Errorf("the note no longer parses: %v", err)
	}
	n, _ = Parse(src)
	if _, err = SetDispatch(n, src, "d16", d); err == nil {
		t.Error("no prompt, no prose, no label: no error")
	}
}

// TestSetDispatchMatchesRender: on an item with a prompt and nothing else, SetDispatch and
// the renderer write the same paragraph, marker and all (#168).
func TestSetDispatchMatchesRender(t *testing.T) {
	t.Parallel()
	nob := testRenderer
	nob.CommandID = ""
	d := Dispatch{Pane: "w1:p1", Workspace: "/ws/o/r/issue-1"}
	item := func(dispatch bool) string {
		u := ItemUnit{
			ID: "a", Icon: IconTodo, Heading: true, Title: "A",
			Button: "send to r", Prompt: &PromptUnit{To: "r", Body: "go"},
		}
		if dispatch {
			u.Dispatch = &d
		}
		b, err := nob.Item(u)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	src := []byte("# Worklog — 2026-10-18\n\n**09:00 UTC:** x.\n\n## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Agent queue\n\n" + item(false))
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := SetDispatch(n, src, "a", d)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(string(src), item(false), item(true), 1); string(out) != want {
		t.Errorf("SetDispatch:\n%s\nwant\n%s", out, want)
	}
}

// TestItemsByRef finds work items by the ref link leading their title, an issue or a PR,
// whatever the link's label.
func TestItemsByRef(t *testing.T) {
	t.Parallel()
	src := []byte(dispatchNote + "\n" + `#### ⬜ Waiting

- ⬜ [r#12](https://github.com/o/r/pull/12): its PR ^pr12
- ⬜ [other#11](https://github.com/x/other/issues/11): elsewhere ^x11
- ⬜ see [r#11](https://github.com/o/r/issues/11) later ^late
- ⬜ [#11](https://example.com/o/r/issues/11): not GitHub ^nogh
`)
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[Ref][]string{
		{Repo: "o/r", N: 11}:     {"d11"},
		{Repo: "o/r", N: 12}:     {"d12", "pr12"},
		{Repo: "x/other", N: 11}: {"x11"},
		{Repo: "o/r", N: 99}:     nil,
	} {
		var got []string
		for _, it := range n.ItemsByRef(ref) {
			got = append(got, it.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %v, want %v", ref, got, want)
		}
	}
}

// TestRenderDispatch checks an item rendered with a marker, in either form, parses back to it.
func TestRenderDispatch(t *testing.T) {
	t.Parallel()
	d := &Dispatch{Pane: "w1:p3", Workspace: "/ws/o/r/issue-11"}
	list, err := testRenderer.Item(ItemUnit{ID: "a", Icon: IconInProgress, Title: "list", Summary: "s", Dispatch: d})
	if err != nil {
		t.Fatal(err)
	}
	if want := "- 🔄 list — s <!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 --> ^a\n"; string(list) != want {
		t.Errorf("list form %q", list)
	}
	for _, body := range []string{"", "Some prose."} {
		heading, err := testRenderer.Item(ItemUnit{
			ID: "b", Icon: IconInProgress, Title: "heading", Heading: true, Body: body,
			Prompt: &PromptUnit{To: "r", Body: "go"}, Button: "send to r", Dispatch: d,
		})
		if err != nil {
			t.Fatal(err)
		}
		src := []byte(strings.Replace(dispatchNote, "- ⬜ [r#12](https://github.com/o/r/issues/12): refresh ^d12\n",
			"- ⬜ x ^d12\n"+string(list)+"\n"+string(heading), 1))
		n, err := Parse(src)
		if err != nil {
			t.Fatalf("%v\n%s", err, src)
		}
		for _, id := range []string{"a", "b"} {
			if it := n.Item(id); it == nil || it.Dispatch == nil || *it.Dispatch != *d {
				t.Errorf("body %q: ^%s does not parse back:\n%s", body, id, src)
			}
		}
	}
	if _, err := testRenderer.Item(ItemUnit{ID: "a", Icon: IconTodo, Title: "x", Dispatch: &Dispatch{Workspace: "x"}}); err == nil {
		t.Error("an invalid marker renders")
	}
}

// TestTransitionKeepsDispatch checks a transition with a new summary keeps the marker of a list
// item, whose title line it rewrites, and of a ##### one, whose paragraph it leaves alone.
func TestTransitionKeepsDispatch(t *testing.T) {
	t.Parallel()
	src := []byte(dispatchNote)
	for _, id := range []string{"d11", "d13"} {
		n, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		out, err := TransitionItem(n, src, id, Waiting, "worker exited")
		if err != nil {
			t.Fatal(err)
		}
		m, err := Parse(out)
		if err != nil {
			t.Fatalf("%s: %v\n%s", id, err, out)
		}
		it := m.Item(id)
		if it.Icon != IconWaiting || it.Dispatch == nil || *it.Dispatch != *n.Item(id).Dispatch {
			t.Errorf("^%s: icon %s dispatch %+v\n%s", id, it.Icon, it.Dispatch, out)
		}
	}
	n, _ := Parse(src)
	out, _ := TransitionItem(n, src, "d11", Waiting, "worker exited")
	if !strings.Contains(string(out), "- ⏳ [#11](https://github.com/o/r/issues/11): dispatch — worker exited <!-- qm pane=w1:p3 ws=/ws/o/r/issue-11 --> ^d11\n") {
		t.Errorf("list item line:\n%s", out)
	}
}

// TestDispatchMarkerRoundTrip renders a marker into an item's line and parses it back; a
// transition with no summary keeps it.
func TestDispatchMarkerRoundTrip(t *testing.T) {
	t.Parallel()
	d := Dispatch{Pane: "w3:p7", Workspace: "/home/u/.local/share/quartermaster/worklog/workspaces/o/r/issue-9"}
	src := []byte("# Worklog — 2026-10-18\n\n## 🔄 o/r\n\n### 🔄 What remains\n\n#### 🔄 Agent queue\n\n" +
		"- ⬜ [r#9](https://github.com/o/r/issues/9): fix it " + Renderer{}.DispatchMarker(d) + " ^fix\n")
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := n.Item("fix").Dispatch; got == nil || *got != d {
		t.Fatalf("dispatch %+v, want %+v", got, d)
	}
	out, err := TransitionItem(n, src, "fix", InProgress, "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err = Parse(out); err != nil {
		t.Fatal(err)
	}
	if got := n.Item("fix").Dispatch; got == nil || *got != d {
		t.Fatalf("after a transition: dispatch %+v, want %+v\n%s", got, d, out)
	}
}
