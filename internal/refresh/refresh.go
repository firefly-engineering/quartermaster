// Package refresh reconciles a day's note with GitHub and herdr (ADR 0003, spec §6 and §11):
// it moves a work item only where the evidence proves the move, and says why it left every
// other item it checked as it was.
//
// Refresh runs in two steps, so nothing outside the note runs under the note's lock:
// [Reconciler.Gather] reads a parsed note and asks gh and herdr about it, and [Plan.Apply],
// inside the engine's Mutate edit, applies what the evidence proved to the note as it then is,
// skipping an item that changed meanwhile. Ticked checkboxes need nothing here: every write
// reconciles them (spec §4.4), and so does refresh's, even when it moves nothing.
//
// The evidence, per work item that is not DONE or DROPPED:
//
//   - GitHub, for an item whose title leads with a link to an issue or PR: a merged PR is
//     DONE; an issue closed as completed is DONE, with the commit or PR that closed it; one
//     closed as not planned or as a duplicate is DROPPED. An open one, a PR closed unmerged or
//     an issue closed with no reason proves nothing.
//   - herdr, for an IN_PROGRESS item with a dispatch marker, which no transition has moved
//     since its dispatch: its pane gone, or no agent in it, is a worker that exited, and the
//     item goes to WAITING, never DONE.
//   - herdr, for an item in *Sent to other repos* marked **sent** or **acked**: the agent it
//     was sent to, once settled, has replied when its output after the item's prompt holds
//     GitHub links; the item is marked **acked** and the new links are added to its line. Its
//     state stays: only GitHub proves the work done, and when it does, the item's status
//     becomes **done** along with its state.
//
// Like every package but main, this one reads neither the environment nor the cwd: gh and
// herdr come in as Runners.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// Reconciler gathers the evidence for a refresh.
type Reconciler struct {
	GH    Runner // runs gh
	Herdr Runner // runs herdr
}

// Applied is one change refresh made to the note.
type Applied struct {
	Item    string   `json:"item" jsonschema:"the work item's id"`
	Ref     string   `json:"ref,omitempty" jsonschema:"its issue or PR, owner/repo#N"`
	From    string   `json:"from" jsonschema:"the state it was in"`
	To      string   `json:"to" jsonschema:"the state it is in; the same as from for a reply"`
	Summary string   `json:"summary,omitempty" jsonschema:"the evidence, written as its summary"`
	Status  string   `json:"status,omitempty" jsonschema:"an item sent to another repo: the status its reply set"`
	Links   []string `json:"links,omitempty" jsonschema:"an item sent to another repo: the links from its reply added to its line"`
}

// Skipped is a work item refresh checked and left as it was.
type Skipped struct {
	Item   string `json:"item" jsonschema:"the work item's id"`
	Ref    string `json:"ref,omitempty" jsonschema:"its issue or PR, owner/repo#N"`
	Reason string `json:"reason" jsonschema:"why it was left as it is: what each check found"`
}

// Plan is the evidence gathered from one read of a note: the changes it proves, and the
// items it proves nothing about.
type Plan struct {
	moves   []move
	skipped []Skipped
}

// move is one proven change: a transition, a sent status (Applied.Status), or both. A reply
// is a status with no transition: state is from.
type move struct {
	Applied
	from      note.State
	state     note.State // the state it moves to
	wasStatus string     // the sent status when read, which Status replaces
}

// gatherer is one Gather's state: what it asked once and reuses.
type gatherer struct {
	Reconciler
	refs       map[note.Ref]evidence
	agentList  []agent
	agentsErr  error
	agentsRead bool
	spaces     map[string][]string
	spacesErr  error
}

// Gather asks GitHub and herdr about every work item of n that is not DONE or DROPPED, in note
// order, and returns what the answers prove.
func (r Reconciler) Gather(ctx context.Context, n *note.Note) Plan {
	g := &gatherer{Reconciler: r, refs: map[note.Ref]evidence{}}
	var p Plan
	for _, pr := range n.Projects {
		for _, pt := range pr.Parts {
			for _, it := range pt.Items {
				g.item(ctx, &p, it, false)
			}
		}
		if pr.Remains == nil {
			continue
		}
		for _, b := range pr.Remains.Buckets {
			for _, it := range b.Items {
				g.item(ctx, &p, it, b.Name == note.SentBucket)
			}
		}
	}
	return p
}

// item checks one work item: GitHub first, as the strongest evidence, then its worker, then a
// reply. The first that proves a change wins.
func (g *gatherer) item(ctx context.Context, p *Plan, it *note.Item, sent bool) {
	from, _ := note.StateOf(it.Icon)
	if from == note.Done || from == note.Dropped {
		return
	}
	head, _ := note.SplitSummary(it.Title)
	l := parseLine(head)
	m := move{Applied: Applied{Item: it.ID, From: from.String()}, from: from}
	if l.ref != (note.Ref{}) {
		m.Ref = l.ref.String()
	}
	var reasons []string
	checked := false
	if l.ref != (note.Ref{}) {
		checked = true
		e := g.github(ctx, l.ref)
		switch {
		case e.to == 0:
			reasons = append(reasons, e.reason)
		case note.Transition(from, e.to, e.summary) != nil:
			reasons = append(reasons, fmt.Sprintf("%s %s, but %s may not move to %s (spec §4.3): unblock it first",
				l.ref, e.summary, from, e.to))
		default:
			m = m.transition(e.to, e.summary)
			if sent && e.to == note.Done && l.status != "" && l.status != statusDone {
				m.wasStatus, m.Status = l.status, statusDone // GitHub proves the ask done
			}
			p.moves = append(p.moves, m)
			return
		}
	}
	if it.Dispatch != nil && from == note.InProgress {
		checked = true
		waiting, reason := g.worker(ctx, it.Dispatch.Pane)
		if waiting != "" {
			p.moves = append(p.moves, m.transition(note.Waiting, waiting))
			return
		}
		reasons = append(reasons, reason)
	}
	if sent {
		checked = true
		switch l.status {
		case statusSent, statusAcked:
			pane, label := "", l.label
			if it.Dispatch != nil {
				pane = it.Dispatch.Pane
			} else if it.Prompt != nil {
				label = it.Prompt.To
			}
			links, reason := g.reply(ctx, it.ID, pane, label, head)
			if len(links) > 0 {
				m = m.transition(from, "")
				m.wasStatus, m.Status, m.Links = l.status, statusAcked, links
				p.moves = append(p.moves, m)
				return
			}
			reasons = append(reasons, reason)
		case statusToSend:
			reasons = append(reasons, "not sent yet")
		case statusDone:
			reasons = append(reasons, "its status is done")
		default:
			reasons = append(reasons, "no **sent** status in its line")
		}
	}
	if checked {
		p.skipped = append(p.skipped, Skipped{Item: it.ID, Ref: m.Ref, Reason: strings.Join(reasons, "; ")})
	}
}

// transition is m as a transition to state s with summary.
func (m move) transition(s note.State, summary string) move {
	m.To, m.state, m.Summary = s.String(), s, summary
	return m
}

// Apply applies the plan to src, the note as it is now, one change at a time and in note
// order. A change to an item that is gone, or whose state, ref or sent status changed since the
// plan's read, is skipped. It returns the new source, what it applied, and every item it
// skipped, the plan's included.
func (p Plan) Apply(src []byte) (out []byte, applied []Applied, skipped []Skipped, err error) {
	out = src
	skipped = append(skipped, p.skipped...)
	for _, m := range p.moves {
		next, err := m.apply(out)
		if err != nil {
			skipped = append(skipped, Skipped{Item: m.Item, Ref: m.Ref, Reason: err.Error()})
			continue
		}
		out = next
		applied = append(applied, m.Applied)
	}
	return out, applied, skipped, nil
}

// apply makes m's change to src, after checking the item still reads as it did.
func (m move) apply(src []byte) ([]byte, error) {
	n, err := note.Parse(src)
	if err != nil {
		return nil, err
	}
	it := n.Item(m.Item)
	if it == nil {
		return nil, errors.New("it is gone since refresh read it")
	}
	if now, _ := note.StateOf(it.Icon); now != m.from {
		return nil, fmt.Errorf("it moved from %s to %s since refresh read it", m.from, now)
	}
	head, _ := note.SplitSummary(it.Title)
	l := parseLine(head)
	if m.Ref != "" && l.ref.String() != m.Ref {
		return nil, fmt.Errorf("its ref changed from %s since refresh read it", m.Ref)
	}
	if m.Status != "" && l.status != m.wasStatus {
		return nil, fmt.Errorf("its status changed from %s to %s since refresh read it", m.wasStatus, l.status)
	}
	if m.state != m.from {
		if src, err = note.TransitionItem(n, src, m.Item, m.state, m.Summary); err != nil {
			return nil, err
		}
		if m.Status == "" {
			return src, nil
		}
		if n, err = note.Parse(src); err != nil {
			return nil, err
		}
		it = n.Item(m.Item)
	}
	return setStatus(src, it, m.Status, m.Links)
}

// setStatus writes status over the sent status in item it's title, and adds links at the end
// of the title, before its summary. The title line is rendered as a transition renders it, so
// its marker, indent and nested lines stay.
func setStatus(src []byte, it *note.Item, status string, links []string) ([]byte, error) {
	head, summary := note.SplitSummary(it.Title)
	l := parseLine(head)
	head = head[:l.at[0]] + status + head[l.at[1]:]
	if len(links) > 0 {
		head += ", " + strings.Join(links, ", ")
	}
	line, err := note.Renderer{}.ItemTitle(note.ItemUnit{ID: it.ID, Icon: it.Icon, Title: head, Summary: summary, Heading: it.Heading})
	if err != nil {
		return nil, err
	}
	return note.Replace(src, note.Span{Start: it.IconSpan.Start, End: it.Head.End}, line), nil
}
