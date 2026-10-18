package note

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file is the one place the note's line formats live: the formats of the worklog skill's
// template.md and its Buttons section. A unit renders to whole lines ending in "\n", ready to
// splice over a span (ADR 0002). Icons render as given; deriving a heading's icon is not the
// renderer's job.

// Renderer renders the units of one note. Its fields build the ▶ button URIs.
type Renderer struct {
	Vault     string // the Obsidian vault's name, for obsidian:// URIs
	Date      string // the note's date, YYYY-MM-DD
	CommandID string // the Shell commands plugin's id (ShellCommandID); "" leaves every button out
}

// StatusUnit is the status line: `**HH:MM UTC:** <text>` or `**HH:MM UTC, <label>:** <text>`.
type StatusUnit struct {
	Time  string // HH:MM, UTC
	Label string // the phase, "" for none
	Text  string // one line: where the day stands
}

// ObjectiveUnit is one row of the objectives table.
type ObjectiveUnit struct {
	Objective string // the goal and its project, `<objective> (<repo>)`
	DoneWhen  string // the checkable condition
	Icon      Icon   // leads Progress
	Progress  string // after the icon, "" for none
}

// NextUnit is one action in *Next*.
type NextUnit struct {
	Box    Box    // a checkbox for a manual action, NoBox otherwise
	Action string // what to do
	Cost   string // "" for none
	Ref    string // the work item or anchor id it details, "" for none
	Button string // the ▶ label after the ▶, as `grill in trunk`; "" for none. Needs Ref.
}

// ItemUnit is an entry in a bucket. With an ID it is a work item; without one it is a one-line
// list entry, as `- ⬜ [#M](url): a decision with no prompt yet`.
type ItemUnit struct {
	ID      string
	Icon    Icon
	Title   string      // one line, after the icon
	Heading bool        // the `#####` form; required with a Prompt
	Body    string      // the `#####` form's prose before its button; no blank line
	Button  string      // the ▶ label, as `grill in trunk`; needs a Prompt
	Prompt  *PromptUnit // the collapsed `> [!prompt]-` callout
}

// PromptUnit is a work item's herdr-prompt block.
type PromptUnit struct {
	To   string // the herdr workspace label
	Body string // the prompt, without callout markers
}

// CarryOverUnit is one row of the carry-over table.
type CarryOverUnit struct {
	Item, State, Verdict string
}

// PartUnit is a `### <icon> Part N — <kind>: <title>` section.
type PartUnit struct {
	Icon  Icon
	N     int
	Kind  string // one of PartKinds
	Title string // as `wave 1, **running**`
	Body  string // markdown under the heading, "" for none
}

// BucketUnit is a `#### <icon> <name>` section; with no items it holds `- —`.
type BucketUnit struct {
	Icon  Icon
	Name  string // one of BucketNames
	Items []ItemUnit
}

// ProjectUnit is a `## <icon> <owner/repo>` section.
type ProjectUnit struct {
	Icon    Icon
	Repo    string
	Parts   []PartUnit
	Remains Icon // the `### <icon> What remains` heading's icon
	Buckets []BucketUnit
}

// Day is a whole note, as day_open writes it.
type Day struct {
	Date       string // YYYY-MM-DD
	Previous   string // the previous note's date, "" for none
	Status     StatusUnit
	Objectives []ObjectiveUnit
	Next       []NextUnit
	Projects   []ProjectUnit
	CarryOver  []CarryOverUnit
	Process    []string // one line each; none renders `- —`
}

// Status renders the status line.
func (r Renderer) Status(u StatusUnit) ([]byte, error) {
	if !isClock(u.Time) {
		return nil, fmt.Errorf("status time %q: want HH:MM", u.Time)
	}
	if err := oneLine("status label", u.Label); err != nil {
		return nil, err
	}
	if strings.ContainsAny(u.Label, "*:") || (u.Label != "" && isBlank(u.Label)) {
		return nil, fmt.Errorf("status label %q: no `*` or `:`, and not blank", u.Label)
	}
	if err := oneLine("status", u.Text); err != nil {
		return nil, err
	}
	head := u.Time + " UTC"
	if u.Label != "" {
		head += ", " + u.Label
	}
	return []byte("**" + head + ":** " + u.Text + "\n"), nil
}

// Objective renders one objectives table row.
func (r Renderer) Objective(u ObjectiveUnit) ([]byte, error) {
	if !u.Icon.isState() {
		return nil, fmt.Errorf("objective %q: progress icon %q is not a status icon", u.Objective, u.Icon)
	}
	progress := string(u.Icon)
	if u.Progress != "" {
		progress += " " + u.Progress
	}
	return tableRow(u.Objective, u.DoneWhen, progress)
}

// CarryOver renders one carry-over table row.
func (r Renderer) CarryOver(u CarryOverUnit) ([]byte, error) {
	return tableRow(u.Item, u.State, u.Verdict)
}

// NextLine renders the n-th action of *Next* (1-based):
// `n. [ ] <action> · <cost> · [[#^<id>|details]] · [▶ <label>](<uri>)`.
func (r Renderer) NextLine(n int, u NextUnit) ([]byte, error) {
	if n < 1 {
		return nil, fmt.Errorf("next: line number %d, want 1 or more", n)
	}
	if u.Action == "" {
		return nil, errors.New("next: empty action")
	}
	if err := oneLine("next action", u.Action+u.Cost); err != nil {
		return nil, err
	}
	if u.Ref != "" && !validID(u.Ref) {
		return nil, fmt.Errorf("next: invalid item id ^%s: lowercase letters, digits and - only", u.Ref)
	}
	if u.Button != "" && u.Box != NoBox {
		return nil, fmt.Errorf("next %q: a manual action has no button: no session can start it", u.Action)
	}
	if u.Button != "" && u.Ref == "" {
		return nil, fmt.Errorf("next %q: a button needs a Ref to send", u.Action)
	}
	box := map[Box]string{NoBox: "", Unchecked: "[ ] ", Checked: "[x] "}[u.Box]
	parts := []string{box + u.Action}
	if u.Cost != "" {
		parts = append(parts, u.Cost)
	}
	if u.Ref != "" {
		parts = append(parts, "[[#^"+u.Ref+"|details]]")
	}
	button, err := r.button(u.Button, u.Ref)
	if err != nil {
		return nil, err
	}
	if button != "" {
		parts = append(parts, button)
	}
	return []byte(strconv.Itoa(n) + ". " + strings.Join(parts, " · ") + "\n"), nil
}

// Item renders a bucket entry. The list form is one line, `- <icon> <title> ^<id>`. The
// heading form is the `#####` heading, a blank line, its paragraph ending with the button and
// `^<id>`, then a blank line and the collapsed prompt callout when it has a prompt. Without a
// CommandID the button is left out and the prompt kept.
func (r Renderer) Item(u ItemUnit) ([]byte, error) {
	what := "entry " + strconv.Quote(u.Title)
	if u.ID != "" {
		what = "work item ^" + u.ID
		if !validID(u.ID) {
			return nil, fmt.Errorf("invalid item id ^%s: lowercase letters, digits and - only", u.ID)
		}
	}
	if !u.Icon.isState() {
		return nil, fmt.Errorf("%s: icon %q is not a status icon", what, u.Icon)
	}
	if u.Title == "" {
		return nil, fmt.Errorf("%s: empty title", what)
	}
	if err := oneLine(what+" title", u.Title); err != nil {
		return nil, err
	}
	if w := strings.Fields(u.Title); strings.HasPrefix(w[len(w)-1], "^") {
		return nil, fmt.Errorf("%s: the title ends in a block id; set ID instead", what)
	}
	if !u.Heading {
		if u.Prompt != nil || u.Body != "" || u.Button != "" {
			return nil, fmt.Errorf("%s: a body, button or prompt needs the ##### form", what)
		}
		line := "- " + string(u.Icon) + " " + u.Title
		if u.ID != "" {
			line += " ^" + u.ID
		}
		return []byte(line + "\n"), nil
	}
	if u.ID == "" {
		return nil, fmt.Errorf("%s: a ##### work item needs an id", what)
	}
	if u.Button != "" && u.Prompt == nil {
		return nil, fmt.Errorf("%s: a button needs a prompt to send", what)
	}
	var para []string
	if body := strings.TrimSuffix(u.Body, "\n"); body != "" {
		para = strings.Split(body, "\n")
		if slices.ContainsFunc(para, isBlank) {
			return nil, fmt.Errorf("%s: a blank line in the body would end its paragraph", what)
		}
	}
	button, err := r.button(u.Button, u.ID)
	if err != nil {
		return nil, err
	}
	if button != "" {
		para = append(para, button)
	}
	if len(para) == 0 {
		para = []string{"^" + u.ID}
	} else {
		para[len(para)-1] += " ^" + u.ID
	}
	var b strings.Builder
	b.WriteString("##### " + string(u.Icon) + " " + u.Title + "\n\n")
	b.WriteString(strings.Join(para, "\n") + "\n")
	if u.Prompt != nil {
		callout, err := prompt(u.ID, *u.Prompt)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		b.WriteString("\n" + callout)
	}
	return []byte(b.String()), nil
}

// Note renders a whole note from d, in template.md's layout. Its buttons carry d.Date, the
// note's own date, whatever r.Date holds.
func (r Renderer) Note(d Day) ([]byte, error) {
	if !isDate(d.Date) {
		return nil, fmt.Errorf("note date %q: want YYYY-MM-DD", d.Date)
	}
	r.Date = d.Date
	if d.Previous != "" && !isDate(d.Previous) {
		return nil, fmt.Errorf("previous date %q: want YYYY-MM-DD", d.Previous)
	}
	var blocks []string
	add := func(b ...string) { blocks = append(blocks, b...) }

	fm := "---\ndate: " + d.Date + "\n"
	if d.Previous != "" {
		fm += "previous: \"[[" + d.Previous + "]]\"\n"
	}
	if len(d.Projects) == 0 {
		fm += "projects: []\n"
	} else {
		fm += "projects:\n"
	}
	for _, p := range d.Projects {
		if !isRepo(p.Repo) {
			return nil, fmt.Errorf("project repo %q: want owner/repo", p.Repo)
		}
		fm += "  - " + p.Repo + "\n"
	}
	add(fm+"tags:\n  - worklog\n---\n", "# Worklog — "+d.Date+"\n")

	status, err := r.Status(d.Status)
	if err != nil {
		return nil, err
	}
	add(string(status))

	objectives := "| Objective | Done when | Progress |\n|---|---|---|\n"
	for _, o := range d.Objectives {
		b, err := r.Objective(o)
		if err != nil {
			return nil, err
		}
		objectives += string(b)
	}
	add("## Objectives\n", objectives)

	add("## Next\n")
	if len(d.Next) > 0 {
		var next string
		for i, u := range d.Next {
			b, err := r.NextLine(i+1, u)
			if err != nil {
				return nil, err
			}
			next += string(b)
		}
		add(next)
	}

	for _, p := range d.Projects {
		b, err := r.project(p)
		if err != nil {
			return nil, err
		}
		add(b...)
	}

	add("## Carry-over\n")
	if d.Previous != "" {
		add("From [[" + d.Previous + "]].\n")
	}
	carry := "| Item | State now | Verdict |\n|---|---|---|\n"
	for _, c := range d.CarryOver {
		b, err := r.CarryOver(c)
		if err != nil {
			return nil, err
		}
		carry += string(b)
	}
	add(carry)

	process, err := list(d.Process, "process")
	if err != nil {
		return nil, err
	}
	add("## Process\n", process)
	return []byte(strings.Join(blocks, "\n")), nil
}

// project renders a project section as blocks, to be joined by blank lines.
func (r Renderer) project(p ProjectUnit) ([]string, error) {
	if !p.Icon.isHeading() {
		return nil, fmt.Errorf("project %s: icon %q", p.Repo, p.Icon)
	}
	blocks := []string{"## " + string(p.Icon) + " " + p.Repo + "\n"}
	for _, pt := range p.Parts {
		if !pt.Icon.isHeading() || pt.N < 1 || !slices.Contains(PartKinds, pt.Kind) {
			return nil, fmt.Errorf("project %s: Part %d: want a heading icon, N ≥ 1 and a kind in %s, got %q %q",
				p.Repo, pt.N, strings.Join(PartKinds, ", "), pt.Icon, pt.Kind)
		}
		if err := oneLine("Part title", pt.Title); err != nil {
			return nil, err
		}
		head := "### " + string(pt.Icon) + " Part " + strconv.Itoa(pt.N) + " — " + pt.Kind
		if pt.Title != "" {
			head += ": " + pt.Title
		}
		blocks = append(blocks, head+"\n")
		if body := strings.TrimSuffix(pt.Body, "\n"); body != "" {
			blocks = append(blocks, body+"\n")
		}
	}
	if len(p.Buckets) == 0 {
		return blocks, nil
	}
	if !p.Remains.isHeading() {
		return nil, fmt.Errorf("project %s: What remains icon %q", p.Repo, p.Remains)
	}
	blocks = append(blocks, "### "+string(p.Remains)+" What remains\n")
	for _, b := range p.Buckets {
		if !slices.Contains(BucketNames, b.Name) {
			return nil, fmt.Errorf("project %s: unknown bucket %q: want one of %s", p.Repo, b.Name, strings.Join(BucketNames, ", "))
		}
		if !b.Icon.isHeading() {
			return nil, fmt.Errorf("project %s: bucket %s: icon %q", p.Repo, b.Name, b.Icon)
		}
		blocks = append(blocks, "#### "+string(b.Icon)+" "+b.Name+"\n")
		if len(b.Items) == 0 {
			blocks = append(blocks, "- —\n")
		}
		// Consecutive list entries form one list; a ##### item is a block of its own.
		var run string
		for _, it := range b.Items {
			unit, err := r.Item(it)
			if err != nil {
				return nil, err
			}
			if !it.Heading {
				run += string(unit)
				continue
			}
			if run != "" {
				blocks, run = append(blocks, run), ""
			}
			blocks = append(blocks, string(unit))
		}
		if run != "" {
			blocks = append(blocks, run)
		}
	}
	return blocks, nil
}

// button renders `[▶ <label>](obsidian://shell-commands/?…)` for item id, or "" when there is
// no label or no command id.
func (r Renderer) button(label, id string) (string, error) {
	if label == "" || r.CommandID == "" {
		return "", nil
	}
	if strings.ContainsAny(label, "[]\n") {
		return "", fmt.Errorf("button label %q: no brackets or newlines", label)
	}
	if r.Vault == "" {
		return "", errors.New("button: no vault name")
	}
	if !isDate(r.Date) {
		return "", fmt.Errorf("button: note date %q: want YYYY-MM-DD", r.Date)
	}
	uri := "obsidian://shell-commands/?vault=" + queryValue(r.Vault) +
		"&execute=" + queryValue(r.CommandID) +
		"&_date=" + r.Date + "&_item=" + id
	return "[▶ " + label + "](" + uri + ")", nil
}

// prompt renders the collapsed callout holding item id's herdr-prompt block.
func prompt(id string, p PromptUnit) (string, error) {
	if p.To == "" || strings.ContainsFunc(p.To, isSpace) {
		return "", fmt.Errorf("prompt to=%q: want a workspace label with no spaces", p.To)
	}
	body := strings.TrimSuffix(p.Body, "\n")
	if strings.TrimSpace(body) == "" {
		return "", errors.New("empty prompt")
	}
	var b strings.Builder
	b.WriteString("> [!prompt]- Prompt sent to " + p.To + "\n")
	b.WriteString("> ```herdr-prompt id=" + id + " to=" + p.To + "\n")
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			return "", fmt.Errorf("prompt line %q would close its ``` fence", line)
		}
		if line == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> " + line + "\n")
		}
	}
	b.WriteString("> ```\n")
	return b.String(), nil
}

// tableRow renders a table row; a bare `|` in a cell is escaped so it stays in its cell.
func tableRow(cells ...string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("|")
	for _, c := range cells {
		if err := oneLine("table cell", c); err != nil {
			return nil, err
		}
		b.WriteString(" ")
		escaped := false // the byte before is an unescaped backslash
		for i := range len(c) {
			if c[i] == '|' && !escaped {
				b.WriteByte('\\')
			}
			escaped = c[i] == '\\' && !escaped
			b.WriteByte(c[i])
		}
		b.WriteString(" |")
	}
	return []byte(b.String() + "\n"), nil
}

// list renders lines as a `- ` list, `- —` when there are none.
func list(lines []string, what string) (string, error) {
	if len(lines) == 0 {
		return "- —\n", nil
	}
	var b strings.Builder
	for _, l := range lines {
		if err := oneLine(what, l); err != nil {
			return "", err
		}
		b.WriteString("- " + l + "\n")
	}
	return b.String(), nil
}

// queryValue percent-encodes a URI query value, a space as %20 rather than +.
func queryValue(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func oneLine(what, s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%s %q: want one line", what, s)
	}
	return nil
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func isClock(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil && len(s) == 5
}

func isDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}
