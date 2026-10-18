package note

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// seedNotes are the notes the splice properties run over.
func seedNotes(t testing.TB) [][]byte {
	t.Helper()
	var out [][]byte
	for _, f := range []string{"2026-10-18.md", "units.md"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	tmpl, ok := t.(*testing.T)
	if ok {
		out = append(out, filledTemplate(tmpl))
	}
	return out
}

// unitSpans lists the span of every unit in n.
func unitSpans(n *Note) []Span {
	var s []Span
	if n.FrontMatter != nil {
		s = append(s, *n.FrontMatter)
	}
	s = append(s, n.Title.Span)
	if n.Status != nil {
		s = append(s, n.Status.Span)
	}
	if n.Objectives != nil {
		s = append(s, n.Objectives.Span)
		for _, r := range n.Objectives.Rows {
			s = append(s, r.Span)
		}
	}
	if n.Next != nil {
		s = append(s, n.Next.Span)
		for _, r := range n.Next.Items {
			s = append(s, r.Span)
		}
	}
	for _, p := range n.Projects {
		s = append(s, p.Span, p.Heading)
		for _, pt := range p.Parts {
			s = append(s, pt.Span, pt.Heading)
			for _, it := range pt.Items {
				s = append(s, it.Span)
			}
		}
		if p.Remains != nil {
			s = append(s, p.Remains.Span, p.Remains.Heading)
			for _, b := range p.Remains.Buckets {
				s = append(s, b.Span, b.Heading)
				for _, it := range b.Items {
					s = append(s, it.Span)
					if it.Prompt != nil {
						s = append(s, it.Prompt.Span)
					}
				}
			}
		}
	}
	if n.CarryOver != nil {
		s = append(s, n.CarryOver.Span)
		for _, r := range n.CarryOver.Rows {
			s = append(s, r.Span)
		}
	}
	if n.Process != nil {
		s = append(s, n.Process.Span)
	}
	for _, a := range n.Actions {
		s = append(s, a.Span)
	}
	for _, a := range n.Anchors {
		s = append(s, a.Span)
	}
	return s
}

// checkNoopSplice is the property: parse → splice(no-op) → bytes is the identity, and a
// replacement leaves every byte outside the span unchanged.
func checkNoopSplice(t *testing.T, src []byte) {
	t.Helper()
	n, err := Parse(src)
	if err != nil {
		return
	}
	for _, s := range unitSpans(n) {
		if got := Replace(src, s, src[s.Start:s.End]); !bytes.Equal(got, src) {
			t.Fatalf("no-op splice of %+v changed the note", s)
		}
		got := Replace(src, s, []byte("X\n"))
		if !bytes.HasPrefix(got, src[:s.Start]) || !bytes.HasSuffix(got, src[s.End:]) ||
			string(got[s.Start:len(got)-(len(src)-s.End)]) != "X\n" {
			t.Fatalf("splice of %+v touched bytes outside it", s)
		}
	}
}

func TestSpliceNoop(t *testing.T) {
	t.Parallel()
	for _, src := range seedNotes(t) {
		checkNoopSplice(t, src)
	}
}

func FuzzSpliceNoop(f *testing.F) {
	for _, src := range seedNotes(f) {
		f.Add(src)
	}
	f.Fuzz(checkNoopSplice)
}

// TestRemoveInsertRoundTrip: removing a unit and inserting it back where it was restores the
// note, for list items, table rows and block work items alike.
func TestRemoveInsertRoundTrip(t *testing.T) {
	t.Parallel()
	for _, src := range seedNotes(t) {
		n, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		var lines, blocks []Span
		for _, p := range n.Projects {
			if p.Remains == nil {
				continue
			}
			for _, b := range p.Remains.Buckets {
				for _, it := range b.Items {
					if it.Heading {
						blocks = append(blocks, it.Span)
					} else {
						lines = append(lines, it.Span)
					}
				}
			}
		}
		if n.CarryOver != nil {
			for _, r := range n.CarryOver.Rows {
				lines = append(lines, r.Span)
			}
		}
		if n.Next != nil {
			for _, r := range n.Next.Items {
				lines = append(lines, r.Span)
			}
		}
		for _, s := range lines {
			unit := src[s.Start:s.End]
			cut := Remove(src, s)
			insert := Insert
			if len(cut) != len(src)-len(unit) {
				insert = InsertBlock // a list's only item is a block of its own
			}
			if got := insert(cut, s.Start, unit); !bytes.Equal(got, src) {
				t.Errorf("line unit %q: remove+insert gave\n%s", unit, got)
			}
		}
		for _, s := range blocks {
			unit := src[s.Start:s.End]
			cut := Remove(src, s)
			if bytes.Contains(cut, []byte("\n\n\n")) {
				t.Errorf("removing %q left a double blank line", unit)
			}
			if got := InsertBlock(cut, s.Start, unit); !bytes.Equal(got, src) {
				t.Errorf("block unit %q: remove+insert gave\n%s", unit, got)
			}
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src, unit, want string
	}{
		{"middle line", "a\nb\nc\n", "b\n", "a\nc\n"},
		{"only list item", "## N\n\n1. a\n\n## P\n", "1. a\n", "## N\n\n## P\n"},
		{"first list item", "## N\n\n1. a\n2. b\n", "1. a\n", "## N\n\n2. b\n"},
		{"block at end", "#### B\n\n- x\n\n##### I\n\ny ^i\n", "##### I\n\ny ^i\n", "#### B\n\n- x\n"},
		{"block in middle", "a\n\nB\n\nc\n", "B\n", "a\n\nc\n"},
		{"whole note", "x\n", "x\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			i := bytes.Index([]byte(c.src), []byte(c.unit))
			s := Span{Start: i, End: i + len(c.unit)}
			if got := string(Remove([]byte(c.src), s)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestInsertBlock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src string
		at        int
		want      string
	}{
		{"after a line", "a\n", 2, "a\n\nB\n"},
		{"before a line", "a\n", 0, "B\n\na\n"},
		{"into a gap", "a\n\nc\n", 3, "a\n\nB\n\nc\n"},
		{"empty note", "", 0, "B\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := string(InsertBlock([]byte(c.src), c.at, []byte("B\n"))); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestSpliceRenderedUnit: a transition rendered as one unit and spliced over its span changes
// that unit and nothing else.
func TestSpliceRenderedUnit(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("testdata", "units.md"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	old := n.Item("impl-4")
	unit, err := testRenderer.Item(ItemUnit{ID: "impl-4", Icon: IconDone, Title: "[#4](https://example.com/4) merged"})
	if err != nil {
		t.Fatal(err)
	}
	// The item's span covers its nested list; keep that and replace the first line only.
	line := Span{Start: old.Start, End: bytes.IndexByte(src[old.Start:], '\n') + old.Start + 1}
	out := Replace(src, line, unit)
	m, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if it := m.Item("impl-4"); it == nil || it.Icon != IconDone || it.Title != "[#4](https://example.com/4) merged" ||
		string(out[it.Start:it.End]) != "- ✅ [#4](https://example.com/4) merged ^impl-4\n  - a note on it\n" {
		t.Errorf("item after splice: %+v", it)
	}
	if !bytes.Equal(out[:line.Start], src[:line.Start]) || !bytes.Equal(out[line.Start+len(unit):], src[line.End:]) {
		t.Error("bytes outside the unit changed")
	}
}
