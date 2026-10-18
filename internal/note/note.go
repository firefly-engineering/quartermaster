// Package note locates every addressable unit of a day's worklog note (spec §4.1) by its
// source byte range. Nothing here renders: per ADR 0002 a mutation splices new bytes over a
// unit's span, so everything else in the note round-trips byte for byte.
package note

import (
	"bytes"
	"fmt"
	"slices"
)

// Span is a unit's place in the source: bytes [Start, End), whole lines, ending with the
// last line's newline (or at the end of a source without one), never on a blank line.
type Span struct {
	Start, End int
	Line       int // 1-based line of Start
}

// Icon is a status icon (spec §4.2).
type Icon string

// The status icons. IconNone marks a heading with nothing under it, as an empty bucket;
// it is never a work item's state.
const (
	IconTodo       Icon = "⬜"
	IconInProgress Icon = "🔄"
	IconInReview   Icon = "🔍"
	IconWaiting    Icon = "⏳"
	IconDone       Icon = "✅"
	IconDropped    Icon = "❌"
	IconNone       Icon = "—"
)

var stateIcons = []Icon{IconTodo, IconInProgress, IconInReview, IconWaiting, IconDone, IconDropped}

// isState reports whether i is a work item's state icon.
func (i Icon) isState() bool { return slices.Contains(stateIcons, i) }

// isHeading reports whether i may lead a section heading.
func (i Icon) isHeading() bool { return i.isState() || i == IconNone }

// Box is a task checkbox's state.
type Box int

// The checkbox states. NoBox: the line has no checkbox.
const (
	NoBox Box = iota
	Unchecked
	Checked
)

// BucketNames are the fixed buckets under a project's *What remains*, in template order.
var BucketNames = []string{
	"Decisions for you",
	ActionsBucket,
	"Agent queue",
	SentBucket,
	"Outside input: chase",
	"Waiting",
}

// ActionsBucket is the bucket of manual actions only the user can do.
const ActionsBucket = "Your actions"

// SentBucket is the bucket of work sent to another repo's session.
const SentBucket = "Sent to other repos"

// ProcessSection is the name of the `## Process` section.
const ProcessSection = "Process"

// Empty reports whether a note's source holds only whitespace: such a note counts as missing.
func Empty(src []byte) bool { return len(bytes.TrimSpace(src)) == 0 }

// PartKinds are the kinds a Part heading names.
var PartKinds = []string{"Human session", "Agent batch", "Land"}

// Note is a parsed note. Every pointer is nil and every slice empty when the note lacks
// that unit.
type Note struct {
	FrontMatter *Span // the leading `---` YAML block, both fences included
	Title       Title
	Status      *Status
	Objectives  *Objectives
	Next        *Next
	Projects    []*Project
	CarryOver   *CarryOver
	Process     *Process
	Actions     []ManualAction // every task checkbox, *Next*'s included, in note order
	Anchors     []Anchor

	items map[string]*Item
}

// Item returns the work item with id, or nil.
func (n *Note) Item(id string) *Item { return n.items[id] }

// Title is the `# Worklog — <date>` heading.
type Title struct {
	Span
	Date string // YYYY-MM-DD
}

// Status is the status line: the first paragraph after the title, `**HH:MM UTC:** …` or
// `**HH:MM UTC, <label>:** …`.
type Status struct {
	Span
	Time  string // HH:MM
	Label string // "" when the line has none
}

// Objectives is the `## Objectives` section; Rows are its table's rows.
type Objectives struct {
	Span
	Table *Span // its table, header included; nil without one
	Rows  []Objective
}

// Objective is one row of the objectives table. Cells hold their trimmed source.
type Objective struct {
	Span
	Objective, DoneWhen, Progress string
	Icon                          Icon // Progress's leading icon
}

// Next is the `## Next` section; Items are its ordered list's items.
type Next struct {
	Span
	Items []NextItem
}

// NextItem is one action in *Next*.
type NextItem struct {
	Span
	Box Box
	Ref string // the item id of its `[[#^<id>|details]]` link, "" without one
}

// Project is a `## <icon> <owner/repo>` section.
type Project struct {
	Span
	Heading  Span
	Icon     Icon
	IconSpan Span // Icon's own bytes in Heading
	Repo     string
	Parts    []*Part
	Remains  *Remains
}

// Part is a `### <icon> Part N — <kind>: <title>` section.
type Part struct {
	Span
	Heading Span
	Icon    Icon
	N       int
	Kind    string // one of PartKinds
	Title   string // the source after the kind and its `:` (or `,`)
	Items   []*Item
	Lists   []Span // its top-level lists, in note order
}

// Remains is a project's `### <icon> What remains` section.
type Remains struct {
	Span
	Heading  Span
	Icon     Icon
	IconSpan Span // Icon's own bytes in Heading
	Buckets  []*Bucket
}

// Bucket is a `#### <icon> <name>` section under *What remains*.
type Bucket struct {
	Span
	Heading  Span
	Icon     Icon
	IconSpan Span   // Icon's own bytes in Heading
	Name     string // one of BucketNames
	Items    []*Item
	Entries  []Entry // its list items led by a state icon with no `^<id>`, at any depth
	Dashes   []Span  // its `- —` list items, which stand for nothing in it
	Lists    []Span  // its top-level lists, in note order
}

// Entry is a list item that is not a work item or a manual action: a bucket's line led by a
// state icon with no `^<id>`, as `- ⬜ [#M](url): a decision with no prompt yet`, or a line of
// *Process*. Its span covers its nested lists.
type Entry struct {
	Span
	Icon Icon   // its state icon; "" in Process
	Text string // its first block's source after the icon, its lines joined by one space
}

// Item is a work item: a `#####` heading whose paragraph ends with `^<id>` (Heading), or a
// list item with a status icon ending with `^<id>`. A heading item's span runs from the
// heading to the end of its paragraph, or of its prompt callout when it has one; a list
// item's covers its nested lists.
type Item struct {
	Span
	Head     Span // the ##### heading line, or a list item's first block, which holds its `^<id>`
	IconSpan Span // Icon's own bytes in Head
	ID       string
	Icon     Icon
	Title    string // the source after the icon, its lines joined by one space, without the `^<id>` or a list item's dispatch marker
	Heading  bool
	Button   string // a ##### item's ▶ label, after the ▶; "" without a button
	Prompt   *Prompt
	Dispatch *Dispatch // where task_dispatch started an agent on it; nil when it has no marker
	Sent     *Sent     // in *Sent to other repos*, its sent status; nil without one

	idAt   int  // the offset of the `^` of its `^<id>`
	marker Span // its dispatch marker's own bytes, when it has one
}

// Prompt is a work item's `> [!prompt]-` callout and the `herdr-prompt` block inside it.
type Prompt struct {
	Span        // the whole callout
	ID   string // the block's id=, always its work item's id
	To   string // the block's to=: a herdr workspace label
	Body string // the block's content, without the callout's `>` markers
}

// ManualAction is a task list item, `- [ ]` / `- [x]` or ordered `1. [ ]`.
type ManualAction struct {
	Span
	Head    Span // its first block, which holds its box and `^<id>`; Span also covers nested lines
	Checked bool
	Mark    int    // the byte offset of the mark inside its `[ ]`: ` `, `x` or `X`
	ID      string // its `^<id>`, "" without one
	Text    string // the source after the checkbox, without the `^<id>` or a list item's dispatch marker
}

// Anchor is a paragraph in a Part or bucket ending with `^<id>`, a link target that is not a
// work item.
type Anchor struct {
	Span
	ID string
}

// CarryOver is the `## Carry-over` section; Rows are its table's rows.
type CarryOver struct {
	Span
	Table *Span // its table, header included; nil without one
	Rows  []CarryOverRow
}

// CarryOverRow is one row of the carry-over table. Cells hold their trimmed source.
type CarryOverRow struct {
	Span
	Item, State, Verdict string
	Ref                  string // the block id its item's `[[<note>#^<id>|…]]` link targets, "" without one
}

// Process is the `## Process` section and its list.
type Process struct {
	Span
	List    *Span
	Entries []Entry // the list's items but its checkboxes (manual actions) and `—`
}

// ParseError is a unit that cannot be placed, at a 1-based line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }
