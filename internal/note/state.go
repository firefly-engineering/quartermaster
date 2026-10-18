package note

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// State is a work item's state (spec §4.2), carried in the note by its icon.
type State int

// The states, in the order of spec §4.2's table and of stateIcons. The zero State is none of
// them.
const (
	Todo State = iota + 1
	InProgress
	InReview
	Waiting
	Done
	Dropped
)

var stateNames = [...]string{Todo: "TODO", InProgress: "IN_PROGRESS", InReview: "IN_REVIEW",
	Waiting: "WAITING", Done: "DONE", Dropped: "DROPPED"}

func (s State) valid() bool { return s >= Todo && s <= Dropped }

// Icon is the state's icon, "" for an invalid state.
func (s State) Icon() Icon {
	if !s.valid() {
		return ""
	}
	return stateIcons[s-Todo]
}

// String is the state's name in spec §4.2, as `IN_PROGRESS`.
func (s State) String() string {
	if !s.valid() {
		return fmt.Sprintf("State(%d)", int(s))
	}
	return stateNames[s]
}

// StateOf reads a work item's icon. `—` and anything else that is not a state icon is not.
func StateOf(i Icon) (State, bool) {
	if k := slices.Index(stateIcons, i); k >= 0 {
		return Todo + State(k), true
	}
	return 0, false
}

// State reads a manual action: a ticked box is DONE, an unticked one TODO (spec §4.4).
func (a ManualAction) State() State {
	if a.Checked {
		return Done
	}
	return Todo
}

// The transition errors; Transition wraps them with the move it refused.
var (
	ErrIllegalTransition = errors.New("illegal transition")
	ErrReasonRequired    = errors.New("a reason is required")
)

// moves are the named transitions of spec §4.3. Besides them, any open state may go to
// WAITING or DROPPED; DONE and DROPPED are terminal.
var moves = map[State][]State{
	Todo:       {InProgress, Done}, // start, direct close
	InProgress: {InReview},         // review
	InReview:   {Done, InProgress}, // complete, rework
	Waiting:    {InProgress},       // unblock
}

// Transition reports whether a work item may move from one state to another (spec §4.3),
// as nil or an error wrapping ErrIllegalTransition or ErrReasonRequired. A move to WAITING or
// DROPPED needs a summary naming what it waits on or why it was dropped.
func Transition(from, to State, summary string) error {
	needsReason := to == Waiting || to == Dropped
	terminal := from == Done || from == Dropped
	if !from.valid() || !to.valid() || terminal || !needsReason && !slices.Contains(moves[from], to) {
		return fmt.Errorf("%w: %s to %s", ErrIllegalTransition, from, to)
	}
	if needsReason && strings.TrimSpace(summary) == "" {
		return fmt.Errorf("%w: %s to %s", ErrReasonRequired, from, to)
	}
	return nil
}

// derive is the heading rule of spec §4.2: `—` over nothing, ✅ when everything is DONE or
// DROPPED, else the least done icon present, ⏳ before 🔄 before 🔍 before ⬜.
func derive(states []State) Icon {
	if len(states) == 0 {
		return IconNone
	}
	for _, s := range []State{Waiting, InProgress, InReview, Todo} {
		if slices.Contains(states, s) {
			return s.Icon()
		}
	}
	return IconDone
}

// DerivedIcon is a heading's icon as derived from what is under it.
type DerivedIcon struct {
	Heading Span // the heading line, whose icon a write replaces
	Was     Icon // the icon the note has
	Icon    Icon // the icon the rule gives
}

// DeriveIcons derives the icon of every project, Part, *What remains* and bucket heading, in
// note order, from the work items and manual actions in its section (spec §4.2). A write
// applies each one whose Icon differs from Was; derivation never touches a checkbox.
func (n *Note) DeriveIcons() []DerivedIcon {
	var out []DerivedIcon
	deriveSection := func(section, heading Span, was Icon) {
		var states []State
		for _, it := range n.items {
			if within(it.Span, section) {
				states = append(states, mustState(it.Icon))
			}
		}
		for _, a := range n.Actions {
			if within(a.Span, section) {
				states = append(states, a.State())
			}
		}
		out = append(out, DerivedIcon{Heading: heading, Was: was, Icon: derive(states)})
	}
	for _, p := range n.Projects {
		deriveSection(p.Span, p.Heading, p.Icon)
		for _, pt := range p.Parts {
			deriveSection(pt.Span, pt.Heading, pt.Icon)
		}
		if r := p.Remains; r != nil {
			deriveSection(r.Span, r.Heading, r.Icon)
			for _, b := range r.Buckets {
				deriveSection(b.Span, b.Heading, b.Icon)
			}
		}
	}
	return out
}

// mustState reads a parsed work item's icon, which the parser only accepts as a state.
func mustState(i Icon) State {
	s, ok := StateOf(i)
	if !ok {
		panic(fmt.Sprintf("note: work item with non-state icon %q", i))
	}
	return s
}

func within(s, section Span) bool { return s.Start >= section.Start && s.Start < section.End }

// Reconcile keeps each *Next* checkbox and the manual action it points at in step (spec
// §4.4): when either is ticked, the other must be. It returns the boxes to tick, in note
// order; writing `x` at each one's Mark ticks it. It never returns a ticked box, so applying
// it can never untick one.
func (n *Note) Reconcile() []ManualAction {
	if n.Next == nil {
		return nil
	}
	// A pair is the manual action with an id and the *Next* checkboxes pointing at it.
	pairs := map[string][]ManualAction{}
	for _, a := range n.Actions {
		if a.ID != "" && !within(a.Span, n.Next.Span) {
			pairs[a.ID] = append(pairs[a.ID], a)
		}
	}
	for _, it := range n.Next.Items {
		if it.Box == NoBox || len(pairs[it.Ref]) == 0 {
			continue
		}
		// The parser records every checkbox, *Next*'s included, as a manual action.
		i := slices.IndexFunc(n.Actions, func(a ManualAction) bool { return a.Start == it.Start })
		pairs[it.Ref] = append(pairs[it.Ref], n.Actions[i])
	}
	var ticks []ManualAction
	for _, pair := range pairs {
		if len(pair) < 2 || !slices.ContainsFunc(pair, func(a ManualAction) bool { return a.Checked }) {
			continue
		}
		for _, a := range pair {
			if !a.Checked {
				ticks = append(ticks, a)
			}
		}
	}
	slices.SortFunc(ticks, func(a, b ManualAction) int { return a.Mark - b.Mark })
	return ticks
}
