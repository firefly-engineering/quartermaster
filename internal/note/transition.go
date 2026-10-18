package note

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrNoItem is returned by TransitionItem for an id that names no work item or manual action.
var ErrNoItem = errors.New("no such work item")

// TransitionItem moves the work item id of n, parsed from src, to state to (spec §4.3) and
// returns the new source. It is the one place a work item changes state: item_transition calls
// it, and so do refresh and task_dispatch, inside their own engine Mutate edits.
//
// The move must be legal (Transition), with summary as its reason or evidence. Only the
// item's title line is rewritten, from its icon on, with the new icon and summary: its list
// marker, indent, nested lines, prose, button and prompt stay as they are. A list item's title
// written over several lines comes out on one. An empty summary keeps the one the item has.
//
// A manual action with that id can only be ticked, to DONE, since it is the user's: its box is
// ticked, and it takes no summary, having nowhere to show one.
func TransitionItem(n *Note, src []byte, id string, to State, summary string) ([]byte, error) {
	if strings.ContainsAny(summary, "\r\n") {
		return nil, fmt.Errorf("^%s: summary %q: want one line", id, summary)
	}
	summary = strings.TrimSpace(summary)
	it := n.Item(id)
	if it == nil {
		return tick(n, src, id, to, summary)
	}
	from := mustState(it.Icon)
	if err := Transition(from, to, summary); err != nil {
		return nil, fmt.Errorf("^%s: %w", id, err)
	}
	head, was := SplitSummary(it.Title)
	if summary == "" {
		summary = was
	}
	line, err := Renderer{}.ItemTitle(ItemUnit{ID: id, Icon: to.Icon(), Title: head, Summary: summary, Heading: it.Heading, Dispatch: it.Dispatch})
	if err != nil {
		return nil, err
	}
	return Replace(src, Span{Start: it.IconSpan.Start, End: it.Head.End}, line), nil
}

// tick moves the manual action id, outside *Next*, to DONE by ticking its box.
func tick(n *Note, src []byte, id string, to State, summary string) ([]byte, error) {
	a := n.Action(id)
	if a == nil {
		return nil, fmt.Errorf("%w ^%s", ErrNoItem, id)
	}
	if to != Done {
		return nil, fmt.Errorf("^%s is a manual action: it can only be ticked (DONE), not moved to %s", id, to)
	}
	if summary != "" {
		return nil, fmt.Errorf("^%s is a manual action: a checkbox has no summary to write", id)
	}
	if a.Checked {
		return nil, fmt.Errorf("^%s: %w: DONE to DONE, a ticked box has no summary to rewrite", id, ErrIllegalTransition)
	}
	if err := Transition(a.State(), to, ""); err != nil {
		return nil, fmt.Errorf("^%s: %w", id, err)
	}
	return Tick(src, []ManualAction{*a}), nil
}

// Action is the manual action with id outside *Next*, or nil. A *Next* line has no id of its
// own: its details link names the action.
func (n *Note) Action(id string) *ManualAction {
	for i, a := range n.Actions {
		if a.ID == id && (n.Next == nil || !within(a.Span, n.Next.Span)) {
			return &n.Actions[i]
		}
	}
	return nil
}

// Tick returns src with each box ticked, writing `x` at its Mark: the one way a write ticks a
// box (spec §4.4). It never unticks one.
func Tick(src []byte, boxes []ManualAction) []byte {
	out := slices.Clone(src)
	for _, a := range boxes {
		out[a.Mark] = 'x'
	}
	return out
}
