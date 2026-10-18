package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// settle takes an edit's output to the bytes a write puts on disk: it must parse; every
// *Next*/action pair with one side ticked has both ticked (spec §4.4); every derived heading
// icon is recomputed (spec §4.2); and no box ticked in was, the note before the edit, is
// unticked.
func settle(was *note.Note, out []byte) ([]byte, *note.Note, error) {
	n, err := parse(out, "the edit's result")
	if err != nil {
		return nil, nil, err
	}
	if ticks := n.Reconcile(); len(ticks) > 0 {
		out = note.Tick(out, ticks)
		if n, err = parse(out, "the reconciled note"); err != nil {
			return nil, nil, err
		}
	}
	derived := n.DeriveIcons()
	changed := false
	for _, d := range slices.Backward(derived) { // back to front, so earlier spans hold
		if d.Icon == d.Was {
			continue
		}
		if string(out[d.IconSpan.Start:d.IconSpan.End]) != string(d.Was) {
			return nil, nil, fmt.Errorf("line %d: heading has no %s icon", d.Heading.Line, d.Was)
		}
		out = note.Replace(out, d.IconSpan, []byte(d.Icon))
		changed = true
	}
	if changed {
		if n, err = parse(out, "the note with its derived icons"); err != nil {
			return nil, nil, err
		}
	}
	if err := untickGuard(was, n); err != nil {
		return nil, nil, err
	}
	return out, n, nil
}

func parse(src []byte, what string) (*note.Note, error) {
	n, err := note.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", what, ErrUnparseable, err)
	}
	return n, nil
}

// untickGuard refuses a note that unticks a box ticked in was (spec §4.4, ADR 0001). was is
// nil for a new note. A box is known by its `^<id>`; a *Next* line without one by the item
// its details link points at; any other box by its text. A ticked box may be removed, but
// while its key is in the note, it is ticked as often as it was. A box known by its text has
// a new key once reworded, so a write that drops a ticked one may add no open one: it could
// be the dropped box, reworded and unticked.
func untickGuard(was, now *note.Note) error {
	if was == nil {
		return nil
	}
	ticked := map[string]int{}
	open := map[string]int{} // the open boxes known by their text
	for _, a := range was.Actions {
		if a.Checked {
			ticked[boxKey(was, a)]++
		} else if boxKey(was, a) == textKey(a) {
			open[a.Text]++
		}
	}
	for _, a := range now.Actions {
		if a.Checked {
			ticked[boxKey(now, a)]--
		}
	}
	for _, a := range now.Actions {
		if !a.Checked && ticked[boxKey(now, a)] > 0 {
			return fmt.Errorf("line %d: %w: %s", a.Line, ErrUntick, a.Text)
		}
	}
	dropped := false // a ticked box known by its text is gone
	for k, c := range ticked {
		dropped = dropped || (c > 0 && strings.HasPrefix(k, byText))
	}
	if !dropped {
		return nil
	}
	for _, a := range now.Actions {
		if a.Checked || boxKey(now, a) != textKey(a) {
			continue
		}
		if open[a.Text] == 0 {
			return fmt.Errorf("line %d: %w: %s may replace a ticked box with no id", a.Line, ErrUntick, a.Text)
		}
		open[a.Text]--
	}
	return nil
}

// boxKey is how untickGuard knows a box across a write: by its id, by the details link of a
// *Next* line, else by its text (textKey).
func boxKey(n *note.Note, a note.ManualAction) string {
	if a.ID != "" {
		return "^" + a.ID
	}
	if n.Next != nil {
		for _, it := range n.Next.Items {
			if it.Start == a.Start && it.Ref != "" {
				return "next ^" + it.Ref
			}
		}
	}
	return textKey(a)
}

// byText prefixes the key of a box known by its text.
const byText = "text "

func textKey(a note.ManualAction) string { return byText + a.Text }
