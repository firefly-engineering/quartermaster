package note

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

var allStates = []State{Todo, InProgress, InReview, Waiting, Done, Dropped}

// TestTransitionTable walks every (from, to) pair of spec §4.3, with and without a reason.
// Every state may move to itself with a summary, terminal ones included: that rewrites only the
// summary.
func TestTransitionTable(t *testing.T) {
	t.Parallel()
	legal := map[State][]State{
		Todo:       {Todo, InProgress, Done, Dropped, Waiting},
		InProgress: {InProgress, InReview, Done, Waiting, Dropped},
		InReview:   {InReview, Done, InProgress, Waiting, Dropped},
		Waiting:    {InProgress, Done, Waiting, Dropped},
		Done:       {Done},
		Dropped:    {Dropped},
	}
	for _, from := range allStates {
		for _, to := range allStates {
			ok := slices.Contains(legal[from], to)
			needsReason := from == to || to == Waiting || to == Dropped || (from == InProgress || from == Waiting) && to == Done
			t.Run(fmt.Sprintf("%s->%s", from, to), func(t *testing.T) {
				t.Parallel()
				err := Transition(from, to, "https://example.com/pr/1")
				switch {
				case ok && err != nil:
					t.Errorf("legal move refused: %v", err)
				case !ok && !errors.Is(err, ErrIllegalTransition):
					t.Errorf("illegal move: got %v, want ErrIllegalTransition", err)
				}
				for _, blank := range []string{"", " \t\n"} {
					err := Transition(from, to, blank)
					switch {
					case !ok && !errors.Is(err, ErrIllegalTransition):
						t.Errorf("illegal move, summary %q: got %v", blank, err)
					case ok && needsReason && !errors.Is(err, ErrReasonRequired):
						t.Errorf("summary %q: got %v, want ErrReasonRequired", blank, err)
					case ok && !needsReason && err != nil:
						t.Errorf("summary %q: %v", blank, err)
					}
				}
			})
		}
	}
}

func TestCarryOverRowOpen(t *testing.T) {
	t.Parallel()
	for verdict, want := range map[string]bool{
		"": true, "defer": true, "defer: device": true, "  Defer ": true, "later": true,
		"keep": false, "Keep: today": false, "kept": false, "drop": false, "drop: gone": false,
		"dropped": false, "done": false,
	} {
		if got := (CarryOverRow{Verdict: verdict}).Open(); got != want {
			t.Errorf("verdict %q: open %v, want %v", verdict, got, want)
		}
	}
}

func TestTransitionUnknownState(t *testing.T) {
	t.Parallel()
	for _, c := range [][2]State{{0, Todo}, {Todo, 0}, {Todo, 99}} {
		if err := Transition(c[0], c[1], "x"); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%v: got %v", c, err)
		}
	}
}

func TestStateIcons(t *testing.T) {
	t.Parallel()
	icons := []Icon{IconTodo, IconInProgress, IconInReview, IconWaiting, IconDone, IconDropped}
	names := []string{"TODO", "IN_PROGRESS", "IN_REVIEW", "WAITING", "DONE", "DROPPED"}
	for i, s := range allStates {
		if s.Icon() != icons[i] || s.String() != names[i] {
			t.Errorf("%d: %s %s", i, s.Icon(), s)
		}
		if got, ok := StateOf(icons[i]); !ok || got != s {
			t.Errorf("StateOf(%s) = %v, %v", icons[i], got, ok)
		}
	}
	if _, ok := StateOf(IconNone); ok {
		t.Error("— read as a state")
	}
	if _, ok := StateOf("x"); ok {
		t.Error("x read as a state")
	}
}

func TestDeriveRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []State
		want Icon
	}{
		{nil, IconNone},
		{[]State{Done}, IconDone},
		{[]State{Dropped}, IconDone},
		{[]State{Done, Dropped}, IconDone},
		{[]State{Todo}, IconTodo},
		{[]State{Done, Todo}, IconInProgress},
		{[]State{Dropped, Todo}, IconInProgress},
		{[]State{Done, InReview}, IconInProgress},
		{[]State{Todo, InReview}, IconInReview},
		{[]State{InReview, InProgress, Todo}, IconInProgress},
		{[]State{InProgress, Waiting, Done}, IconWaiting},
		{[]State{Waiting, Todo, InReview, InProgress, Dropped}, IconWaiting},
	}
	for _, c := range cases {
		if got := derive(c.in); got != c.want {
			t.Errorf("derive(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

const derivedNote = `# Worklog — 2026-10-18

**09:00 UTC:** open.

## Next

1. [ ] Rotate · [[#^rotate|details]]

## ⬜ o/r

### ⬜ Part 1 — Human session: decisions, done

- ✅ Decided ^d-1
- ❌ Dropped it ^d-2

### ✅ Part 2 — Land: nothing tracked

Merged at noon. ^anchor

### ✅ What remains

#### ✅ Decisions for you

- —

#### ✅ Your actions

- [x] Rotate ^rotate
- [ ] Renew the cert

#### ✅ Agent queue

##### ✅ Batch

Run it. ^batch

- 🔍 [#4](https://example.com/4) ^impl-4

#### ✅ Waiting

- ⏳ On IaC ^iac

## ✅ p/q

### ⬜ Part 1 — Agent batch: all done

- [x] Merged by hand
- ✅ Shipped ^ship
`

func TestDeriveIcons(t *testing.T) {
	t.Parallel()
	src := []byte(derivedNote)
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := n.DeriveIcons()
	want := []struct {
		heading  string
		was, now Icon
	}{
		{"## ⬜ o/r\n", IconTodo, IconWaiting},
		{"### ✅ What remains\n", IconDone, IconWaiting},
		{"#### ✅ Decisions for you\n", IconDone, IconNone},
		{"#### ✅ Your actions\n", IconDone, IconInProgress},
		{"#### ✅ Agent queue\n", IconDone, IconInProgress},
		{"#### ✅ Waiting\n", IconDone, IconWaiting},
		{"## ✅ p/q\n", IconDone, IconInProgress}, // Part 1's written ⬜ counts
	}
	if len(got) != len(want) {
		t.Fatalf("got %d headings: %+v", len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if at(src, g.Heading) != w.heading || g.Was != w.was || g.Icon != w.now {
			t.Errorf("%d: %q %s→%s, want %q %s→%s", i, at(src, g.Heading), g.Was, g.Icon, w.heading, w.was, w.now)
		}
	}
}

// TestDeriveIconsSpan: each derived heading's IconSpan covers exactly its icon's bytes, past
// however much space follows the `#` run, `—` included.
func TestDeriveIconsSpan(t *testing.T) {
	t.Parallel()
	src := []byte("# Worklog — 2026-10-18\n\n" +
		"## — o/r\n\n" +
		"###   ⬜ What remains\n\n" +
		"####\t— Waiting\n\n" +
		"####  ✅ Agent queue\n\n- ⏳ On IaC ^iac\n\n" + // goldmark keeps the NBSP
		"##  🔄 p/q\n")
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Icon{IconNone, IconTodo, IconNone, IconDone, IconInProgress}
	got := n.DeriveIcons()
	if len(got) != len(want) {
		t.Fatalf("got %d headings: %+v", len(got), got)
	}
	for i, d := range got {
		if s := at(src, d.IconSpan); s != string(want[i]) || s != string(d.Was) ||
			!within(d.IconSpan, d.Heading) || d.IconSpan.Line != d.Heading.Line {
			t.Errorf("%d: IconSpan %+v is %q in %q, want %s", i, d.IconSpan, s, at(src, d.Heading), want[i])
		}
	}
}

// TestDeriveIconsUnits: the hand-written icons of units.md already follow the rule.
func TestDeriveIconsUnits(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("testdata", "units.md"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := n.DeriveIcons()
	if len(got) != 4 { // Part icons are written, not derived
		t.Fatalf("headings: %+v", got)
	}
	for _, d := range got {
		if d.Icon != d.Was {
			t.Errorf("%q: derived %s", at(src, d.Heading), d.Icon)
		}
	}
}

// boxNote pairs each *Next* checkbox with a manual action, one pair per combination of ticks.
func boxNote(next, action [4]bool) string {
	mark := func(b bool) string {
		if b {
			return "x"
		}
		return " "
	}
	ids := []string{"a", "b", "c", "d"}
	s := "# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n## Next\n\n"
	for i, id := range ids {
		s += fmt.Sprintf("%d. [%s] Act %s · [[#^%s|details]]\n", i+1, mark(next[i]), id, id)
	}
	s += "5. [ ] Not a pair · [[#^item|details]]\n"
	s += "\n## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Your actions\n\n"
	for i, id := range ids {
		s += fmt.Sprintf("- [%s] Act %s ^%s\n", mark(action[i]), id, id)
	}
	s += "- [ ] Unpaired\n\n#### ⬜ Agent queue\n\n- ⬜ An item ^item\n"
	return s
}

// TestReconcile: ticking either side of a pair ticks both, and nothing else changes.
func TestReconcile(t *testing.T) {
	t.Parallel()
	src := []byte(boxNote([4]bool{false, true, false, true}, [4]bool{false, false, true, true}))
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var got []string
	for _, a := range n.Reconcile() {
		got = append(got, at(src, a.Span))
	}
	want := []string{"3. [ ] Act c · [[#^c|details]]\n", "- [ ] Act b ^b\n"}
	if !slices.Equal(got, want) {
		t.Errorf("ticks %q, want %q", got, want)
	}
}

// TestCheckboxInvariant: over every combination of ticks, applying the reconciliation never
// unticks a box, and leaves every pair agreeing.
func TestCheckboxInvariant(t *testing.T) {
	t.Parallel()
	for mask := range 1 << 8 {
		var next, action [4]bool
		for i := range 4 {
			next[i] = mask&(1<<i) != 0
			action[i] = mask&(1<<(i+4)) != 0
		}
		src := []byte(boxNote(next, action))
		n, err := Parse(src)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		out := slices.Clone(src)
		for _, a := range n.Reconcile() {
			if a.Checked {
				t.Errorf("mask %08b: tick on a ticked box %+v", mask, a)
			}
			out[a.Mark] = 'x'
		}
		m, err := Parse(out)
		if err != nil {
			t.Fatalf("mask %08b: reparse: %v", mask, err)
		}
		if len(m.Actions) != len(n.Actions) {
			t.Fatalf("mask %08b: actions %d → %d", mask, len(n.Actions), len(m.Actions))
		}
		for i, a := range n.Actions {
			if a.Checked && !m.Actions[i].Checked {
				t.Errorf("mask %08b: unticked %+v", mask, a)
			}
		}
		for i := range 4 {
			want := next[i] || action[i]
			if (m.Next.Items[i].Box == Checked) != want || m.Actions[i].Checked != want || m.Actions[5+i].Checked != want {
				t.Errorf("mask %08b: pair %d not in step", mask, i)
			}
		}
		if m.Actions[4].Checked || m.Actions[9].Checked {
			t.Errorf("mask %08b: ticked an unpaired box", mask)
		}
		if again := m.Reconcile(); len(again) != 0 {
			t.Errorf("mask %08b: not idempotent: %+v", mask, again)
		}
	}
}

func TestCheckboxStates(t *testing.T) {
	t.Parallel()
	if s := (ManualAction{Checked: true}).State(); s != Done {
		t.Errorf("ticked: %s", s)
	}
	if s := (ManualAction{}).State(); s != Todo {
		t.Errorf("unticked: %s", s)
	}
}

// TestBoxMark: every manual action's Mark is the mark inside its own `[ ]`, whatever its list
// marker, indentation or nesting.
func TestBoxMark(t *testing.T) {
	t.Parallel()
	src := []byte("# Worklog — 2026-10-18\n\n**09:00 UTC:** open.\n\n## Next\n\n" +
		"1. [ ] One\n2. [x] Two\n\n## ⬜ o/r\n\n### ⬜ What remains\n\n#### ⬜ Your actions\n\n" +
		"- [ ] a ^a\n  * [X] nested\n    1.\t[ ] deeper\n\n12) [x] paren\n")
	n, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var got []byte
	for _, a := range n.Actions {
		if a.Mark < a.Start || a.Mark >= a.End || src[a.Mark-1] != '[' || src[a.Mark+1] != ']' {
			t.Errorf("%q: Mark %d", at(src, a.Span), a.Mark)
			continue
		}
		got = append(got, src[a.Mark])
	}
	if string(got) != " x X x" || len(n.Actions) != 6 {
		t.Errorf("marks %q over %d actions", got, len(n.Actions))
	}
}
