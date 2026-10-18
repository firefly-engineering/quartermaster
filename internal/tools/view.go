package tools

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// NoteView is a note as structured data: what day_read returns.
type NoteView struct {
	Date          string          `json:"date" jsonschema:"the note's date, YYYY-MM-DD, from its title"`
	Status        *StatusView     `json:"status,omitempty" jsonschema:"the status line, absent when the note has none"`
	Objectives    []ObjectiveView `json:"objectives"`
	Next          []NextView      `json:"next" jsonschema:"the ordered actions of Next"`
	Projects      []ProjectView   `json:"projects"`
	CarryOver     []CarryOverView `json:"carry_over"`
	Process       []EntryView     `json:"process" jsonschema:"the Process list, without its - — placeholder"`
	ManualActions []ManualAction  `json:"manual_actions" jsonschema:"every task checkbox, Next's included, in note order"`
	Anchors       []AnchorView    `json:"anchors"`
}

// StatusView is the status line.
type StatusView struct {
	Line  int    `json:"line"`
	Time  string `json:"time" jsonschema:"HH:MM, UTC"`
	Label string `json:"label,omitempty" jsonschema:"the phase it names"`
	Text  string `json:"text" jsonschema:"the whole line, as written"`
}

// ObjectiveView is one objectives row.
type ObjectiveView struct {
	Line      int    `json:"line"`
	Objective string `json:"objective"`
	DoneWhen  string `json:"done_when"`
	Progress  string `json:"progress" jsonschema:"the progress cell, its icon first"`
	State     string `json:"state" jsonschema:"the progress icon's state: TODO, IN_PROGRESS, IN_REVIEW, WAITING, DONE or DROPPED"`
}

// NextView is one action of Next.
type NextView struct {
	Line int    `json:"line"`
	Text string `json:"text" jsonschema:"the line, as written"`
	Box  string `json:"box,omitempty" jsonschema:"checked or unchecked for a manual action, absent otherwise"`
	Ref  string `json:"ref,omitempty" jsonschema:"the work item or anchor id its details link names"`
}

// ProjectView is one project section.
type ProjectView struct {
	Line    int          `json:"line"`
	Repo    string       `json:"repo" jsonschema:"owner/repo"`
	Icon    string       `json:"icon"`
	Parts   []PartView   `json:"parts"`
	Remains *RemainsView `json:"remains,omitempty" jsonschema:"What remains, absent when the project has none"`
}

// PartView is one Part.
type PartView struct {
	Line  int        `json:"line"`
	Icon  string     `json:"icon"`
	N     int        `json:"n"`
	Kind  string     `json:"kind" jsonschema:"Human session, Agent batch or Land"`
	Title string     `json:"title"`
	Items []ItemView `json:"items" jsonschema:"its work items, those with an id"`
}

// RemainsView is a project's What remains.
type RemainsView struct {
	Line    int          `json:"line"`
	Icon    string       `json:"icon"`
	Buckets []BucketView `json:"buckets"`
}

// BucketView is one bucket.
type BucketView struct {
	Line  int        `json:"line"`
	Icon  string     `json:"icon"`
	Name  string     `json:"name"`
	Items []ItemView `json:"items" jsonschema:"its work items, and its entries led by a state icon with no id"`
}

// ItemView is a work item, or a bucket entry led by a state icon with no id.
type ItemView struct {
	Line    int         `json:"line"`
	ID      string      `json:"id,omitempty" jsonschema:"the item id, absent for an entry with none"`
	Icon    string      `json:"icon"`
	State   string      `json:"state"`
	Title   string      `json:"title"`
	Heading bool        `json:"heading,omitempty" jsonschema:"the ##### form"`
	Prompt  *PromptView `json:"prompt,omitempty"`
}

// PromptView is a work item's herdr-prompt block.
type PromptView struct {
	To   string `json:"to" jsonschema:"the herdr workspace label"`
	Body string `json:"body"`
}

// CarryOverView is one carry-over row.
type CarryOverView struct {
	Line    int    `json:"line"`
	Item    string `json:"item"`
	State   string `json:"state"`
	Verdict string `json:"verdict"`
}

// EntryView is one list entry.
type EntryView struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// ManualAction is a task checkbox.
type ManualAction struct {
	Line    int    `json:"line"`
	ID      string `json:"id,omitempty"`
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

// AnchorView is a link target with no state.
type AnchorView struct {
	Line int    `json:"line"`
	ID   string `json:"id"`
}

// ParseErrorView is a unit the parser could not place.
type ParseErrorView struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// view builds the structured view of a parsed note n with source src.
func view(n *note.Note, src []byte) NoteView {
	v := NoteView{
		Date:          n.Title.Date,
		Objectives:    []ObjectiveView{},
		Next:          []NextView{},
		Projects:      []ProjectView{},
		CarryOver:     []CarryOverView{},
		Process:       []EntryView{},
		ManualActions: []ManualAction{},
		Anchors:       []AnchorView{},
	}
	if s := n.Status; s != nil {
		v.Status = &StatusView{Line: s.Line, Time: s.Time, Label: s.Label, Text: source(src, s.Span)}
	}
	if n.Objectives != nil {
		for _, o := range n.Objectives.Rows {
			v.Objectives = append(v.Objectives, ObjectiveView{
				Line: o.Line, Objective: o.Objective, DoneWhen: o.DoneWhen, Progress: o.Progress, State: stateName(o.Icon),
			})
		}
	}
	if n.Next != nil {
		for _, it := range n.Next.Items {
			v.Next = append(v.Next, nextView(src, it))
		}
	}
	for _, p := range n.Projects {
		pv := ProjectView{Line: p.Line, Repo: p.Repo, Icon: string(p.Icon), Parts: []PartView{}}
		for _, pt := range p.Parts {
			items := []ItemView{}
			for _, it := range pt.Items {
				items = append(items, itemView(it))
			}
			pv.Parts = append(pv.Parts, PartView{
				Line: pt.Line, Icon: string(pt.Icon), N: pt.N, Kind: pt.Kind, Title: pt.Title, Items: items,
			})
		}
		if r := p.Remains; r != nil {
			rv := &RemainsView{Line: r.Line, Icon: string(r.Icon), Buckets: []BucketView{}}
			for _, b := range r.Buckets {
				rv.Buckets = append(rv.Buckets, BucketView{
					Line: b.Line, Icon: string(b.Icon), Name: b.Name, Items: bucketItems(b),
				})
			}
			pv.Remains = rv
		}
		v.Projects = append(v.Projects, pv)
	}
	v.CarryOver = carryOverViews(n)
	if n.Process != nil {
		for _, e := range n.Process.Entries {
			v.Process = append(v.Process, EntryView{Line: e.Line, Text: e.Text})
		}
	}
	for _, a := range n.Actions {
		v.ManualActions = append(v.ManualActions, ManualAction{Line: a.Line, ID: a.ID, Text: a.Text, Checked: a.Checked})
	}
	for _, a := range n.Anchors {
		v.Anchors = append(v.Anchors, AnchorView{Line: a.Line, ID: a.ID})
	}
	return v
}

// carryOverViews are the note's carry-over rows, empty when it has none.
func carryOverViews(n *note.Note) []CarryOverView {
	out := []CarryOverView{}
	if n.CarryOver != nil {
		for _, r := range n.CarryOver.Rows {
			out = append(out, CarryOverView{Line: r.Line, Item: r.Item, State: r.State, Verdict: r.Verdict})
		}
	}
	return out
}

func itemView(it *note.Item) ItemView {
	iv := ItemView{
		Line: it.Line, ID: it.ID, Icon: string(it.Icon), State: stateName(it.Icon), Title: it.Title, Heading: it.Heading,
	}
	if it.Prompt != nil {
		iv.Prompt = &PromptView{To: it.Prompt.To, Body: it.Prompt.Body}
	}
	return iv
}

// bucketItems are a bucket's work items and its entries (state icon, no id), in note order.
func bucketItems(b *note.Bucket) []ItemView {
	out := []ItemView{}
	for _, it := range b.Items {
		out = append(out, itemView(it))
	}
	for _, e := range b.Entries {
		out = append(out, ItemView{Line: e.Line, Icon: string(e.Icon), State: stateName(e.Icon), Title: e.Text})
	}
	slices.SortStableFunc(out, func(a, b ItemView) int { return cmp.Compare(a.Line, b.Line) })
	return out
}

// stateName is an icon's state name, "" when the icon is not a state.
func stateName(i note.Icon) string {
	if s, ok := note.StateOf(i); ok {
		return s.String()
	}
	return ""
}

// source is a span's source with its trailing newline trimmed.
func source(src []byte, s note.Span) string {
	return strings.TrimRight(string(src[s.Start:s.End]), "\r\n")
}

// parseErrors flattens Parse's joined error into its *note.ParseError units.
func parseErrors(err error) []ParseErrorView {
	out := []ParseErrorView{}
	if err == nil {
		return out
	}
	errs := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	}
	for _, e := range errs {
		var pe *note.ParseError
		if errors.As(e, &pe) {
			out = append(out, ParseErrorView{Line: pe.Line, Message: pe.Msg})
		} else {
			out = append(out, ParseErrorView{Message: e.Error()})
		}
	}
	return out
}
