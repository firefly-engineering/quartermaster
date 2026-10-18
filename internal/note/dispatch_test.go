package note

import "testing"

// dispatchNote holds dispatched work items in both forms, and items whose comments are not
// dispatch markers.
const dispatchNote = "# Worklog — 2026-10-18\n" + `
## 🔄 o/r

### 🔄 What remains

#### 🔄 Agent queue

##### 🔄 [#14](https://github.com/o/r/issues/14): which store?

Working on it. <!-- qm pane=w1:p3 ws=/ws/o/r/issue-14 --> ^store

- 🔄 [#16](https://github.com/o/r/issues/16): drop IE11 <!-- qm pane=w2:p1 ws=/ws/o/r/issue-16 --> ^ie11
- 🔄 [#17](https://github.com/o/r/issues/17): a plain comment <!-- a note --> ^plain
- 🔄 [#18](https://github.com/o/r/issues/18): a marker with no pane <!-- qm ws=/ws --> ^nopane
- 🔄 [#19](https://github.com/o/r/issues/19): an unknown key <!-- qm pane=w1:p1 ws=/ws tab=w1:t1 --> ^unknown
- 🔄 [#20](https://github.com/o/r/issues/20): in a code span ` + "`<!-- qm pane=w1:p1 ws=/ws -->`" + ` ^code
- 🔄 [#21](https://github.com/o/r/issues/21): no marker ^none
`

func TestDispatchMarker(t *testing.T) {
	t.Parallel()
	n, err := Parse([]byte(dispatchNote))
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]*Dispatch{
		"store":   {Pane: "w1:p3", Workspace: "/ws/o/r/issue-14"},
		"ie11":    {Pane: "w2:p1", Workspace: "/ws/o/r/issue-16"},
		"plain":   nil,
		"nopane":  nil,
		"unknown": nil,
		"code":    nil,
		"none":    nil,
	} {
		it := n.Item(id)
		if it == nil {
			t.Fatalf("no item ^%s", id)
		}
		switch got := it.Dispatch; {
		case want == nil && got != nil:
			t.Errorf("^%s: dispatch %+v, want none", id, *got)
		case want != nil && (got == nil || *got != *want):
			t.Errorf("^%s: dispatch %+v, want %+v", id, got, *want)
		}
	}
}

// TestDispatchMarkerRoundTrip renders a marker into an item's line and parses it back; a
// transition keeps it.
func TestDispatchMarkerRoundTrip(t *testing.T) {
	t.Parallel()
	d := Dispatch{Pane: "w3:p7", Workspace: "/home/u/.local/share/quartermaster/workspaces/o/r/issue-9"}
	m := Renderer{}.DispatchMarker(d)
	if want := "<!-- qm pane=w3:p7 ws=/home/u/.local/share/quartermaster/workspaces/o/r/issue-9 -->"; m != want {
		t.Fatalf("marker %q, want %q", m, want)
	}
	src := []byte("# Worklog — 2026-10-18\n\n## 🔄 o/r\n\n### 🔄 What remains\n\n#### 🔄 Agent queue\n\n" +
		"- ⬜ [#9](https://github.com/o/r/issues/9): fix it " + m + " ^fix\n")
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
		t.Fatalf("after a transition: dispatch %+v, want %+v", got, d)
	}
}
