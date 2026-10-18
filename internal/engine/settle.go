package engine

import (
	"bytes"
	"fmt"
	"slices"

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
		out = bytes.Clone(out)
		for _, a := range ticks {
			out[a.Mark] = 'x'
		}
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
		if out, err = setIcon(out, d); err != nil {
			return nil, nil, err
		}
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

// setIcon replaces the icon of d's heading, the one after its `#` run, with d.Icon.
func setIcon(src []byte, d note.DerivedIcon) ([]byte, error) {
	at := d.Heading.Start
	for at < d.Heading.End && src[at] == '#' {
		at++
	}
	for at < d.Heading.End && (src[at] == ' ' || src[at] == '\t') {
		at++
	}
	if !bytes.HasPrefix(src[at:d.Heading.End], []byte(d.Was)) {
		return nil, fmt.Errorf("line %d: heading has no %s icon", d.Heading.Line, d.Was)
	}
	return note.Replace(src, note.Span{Start: at, End: at + len(d.Was)}, []byte(d.Icon)), nil
}

// untickGuard refuses a note that unticks a box ticked in was (spec §4.4, ADR 0001). A box is
// known by its `^<id>`, or by its text when it has none. A ticked box may be removed, but
// while its key is in the note, it is ticked as often as it was. was is nil for a new note.
// A box with no id that is reworded and unticked in one write has a new key, and passes: a
// stricter rule would also refuse replacing a done *Next* line with a new open one.
func untickGuard(was, now *note.Note) error {
	if was == nil {
		return nil
	}
	key := func(a note.ManualAction) string {
		if a.ID != "" {
			return "^" + a.ID
		}
		return a.Text
	}
	ticked := map[string]int{}
	for _, a := range was.Actions {
		if a.Checked {
			ticked[key(a)]++
		}
	}
	for _, a := range now.Actions {
		if a.Checked {
			ticked[key(a)]--
		}
	}
	for _, a := range now.Actions {
		if !a.Checked && ticked[key(a)] > 0 {
			return fmt.Errorf("line %d: %w: %s", a.Line, ErrUntick, a.Text)
		}
	}
	return nil
}
