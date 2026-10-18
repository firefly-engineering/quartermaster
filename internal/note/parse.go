package note

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var md = goldmark.New(goldmark.WithExtensions(extension.Table, extension.TaskList))

// Parse locates every unit of the note in src. It always returns the units it could place;
// the error, when non-nil, joins one *ParseError per unit it could not, in line order.
func Parse(src []byte) (*Note, error) {
	p := &parser{
		src:    src,
		n:      &Note{items: map[string]*Item{}},
		placed: map[ast.Node]bool{},
	}
	for i, b := range src {
		if b == '\n' {
			p.lines = append(p.lines, i+1)
		}
	}
	doc := md.Parser().Parse(text.NewReader(p.frontMatter()))
	p.actions(doc)
	p.structure(doc)
	p.unplaced(doc)
	p.duplicates()
	p.nextRefs()
	return p.n, p.err()
}

// Where the structure walk is.
type section int

const (
	secNone section = iota // before the title
	secPreamble
	secObjectives
	secNext
	secProject
	secCarryOver
	secProcess
	secSkip // an unknown or repeated section, already reported
)

type openSection struct {
	level int
	span  *Span
}

type parser struct {
	src    []byte
	lines  []int // offsets of line starts after the first
	n      *Note
	errs   []*ParseError
	placed map[ast.Node]bool // blocks whose ^id or herdr-prompt fence is accounted for
	ids    []idAt

	sec        section
	beforeSeen bool // content before the title, reported once
	statusSeen bool
	nextList   bool
	objTable   bool
	carryTable bool
	sections   []openSection
	project    *Project
	part       *Part
	remains    *Remains
	bucket     *Bucket
	pending    *Item // a ##### item waiting for its ^id paragraph
}

type idAt struct {
	id   string
	line int
}

func (p *parser) errorf(line int, format string, args ...any) {
	p.errs = append(p.errs, &ParseError{Line: line, Msg: fmt.Sprintf(format, args...)})
}

func (p *parser) err() error {
	slices.SortStableFunc(p.errs, func(a, b *ParseError) int { return cmp.Compare(a.Line, b.Line) })
	errs := make([]error, len(p.errs))
	for i, e := range p.errs {
		errs[i] = e
	}
	return errors.Join(errs...)
}

// frontMatter records the leading `---` block and returns the source with its bytes blanked,
// so goldmark reads blank lines there and every offset stays the source's.
func (p *parser) frontMatter() []byte {
	if len(p.src) == 0 || !isFence(p.src[:p.lineEnd(0)]) {
		return p.src
	}
	for off := p.lineEnd(0); off < len(p.src); {
		end := p.lineEnd(off)
		if isFence(p.src[off:end]) {
			p.n.FrontMatter = &Span{Start: 0, End: end, Line: 1}
			masked := bytes.Clone(p.src)
			for i := range end {
				if masked[i] != '\n' {
					masked[i] = ' '
				}
			}
			return masked
		}
		off = end
	}
	p.errorf(1, "front matter opens with `---` but never closes")
	return p.src
}

// isFence reports whether line, with its LF or CRLF end, is a front matter `---` fence.
func isFence(line []byte) bool {
	return string(bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))) == "---"
}

// Source positions.

func (p *parser) lineOf(off int) int { return sort.SearchInts(p.lines, off+1) + 1 }

func (p *parser) lineStart(off int) int { return bytes.LastIndexByte(p.src[:off], '\n') + 1 }

func (p *parser) lineEnd(off int) int {
	if i := bytes.IndexByte(p.src[off:], '\n'); i >= 0 {
		return off + i + 1
	}
	return len(p.src)
}

// trimBack moves end back over blank lines.
func (p *parser) trimBack(end int) int {
	for end > 0 {
		start := p.lineStart(end - 1)
		if len(bytes.TrimSpace(p.src[start:end])) != 0 {
			break
		}
		end = start
	}
	return end
}

func (p *parser) span(start, end int) Span {
	return Span{Start: start, End: end, Line: p.lineOf(start)}
}

// nodeStart is the start of the line a block opens on.
func (p *parser) nodeStart(n ast.Node) int {
	pos := n.Pos()
	if pos < 0 && n.Lines().Len() > 0 {
		pos = n.Lines().At(0).Start
	}
	return p.lineStart(max(pos, 0))
}

// nodeEnd is where a block ends: before the next block at its level or above, less blank lines.
func (p *parser) nodeEnd(n ast.Node) int {
	for x := n; x != nil; x = x.Parent() {
		if next := x.NextSibling(); next != nil {
			return p.trimBack(p.nodeStart(next))
		}
	}
	return p.trimBack(len(p.src))
}

func (p *parser) blockSpan(n ast.Node) Span { return p.span(p.nodeStart(n), p.nodeEnd(n)) }

func (p *parser) line(n ast.Node) int { return p.lineOf(p.nodeStart(n)) }

// linesText is a block's raw source, inline markup included.
func (p *parser) linesText(n ast.Node) string {
	var b strings.Builder
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		b.Write(seg.Value(p.src))
	}
	return b.String()
}

// inlineText is the text of an inline node's Text descendants.
func (p *parser) inlineText(n ast.Node) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(p.src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// trailingID finds the `^<id>` ending a paragraph or text block. ok reports an id-shaped
// token in plain text at the end; bad explains why that token is not a valid item id.
func (p *parser) trailingID(n ast.Node) (id string, ok bool, bad string) {
	if n.Lines().Len() == 0 {
		return "", false, ""
	}
	last := n.Lines().At(n.Lines().Len() - 1)
	line := strings.TrimRight(string(last.Value(p.src)), " \t\r\n")
	i := strings.LastIndexByte(line, '^')
	if i < 0 || (i > 0 && line[i-1] != ' ' && line[i-1] != '\t') {
		return "", false, ""
	}
	cand := line[i+1:]
	if !only(cand, "-_", true) {
		return "", false, ""
	}
	// The token must be plain text, not inside a code span or a link.
	t, isText := n.LastChild().(*ast.Text)
	if abs := last.Start + i; !isText || abs < t.Segment.Start || abs >= t.Segment.Stop {
		return "", false, ""
	}
	if !ValidID(cand) {
		return cand, true, fmt.Sprintf("invalid item id ^%s: lowercase letters, digits and - only", cand)
	}
	return cand, true, ""
}

// ValidID reports whether s is an item id: lowercase letters, digits and `-` (spec §4.1).
func ValidID(s string) bool { return only(s, "-", false) }

// only reports whether s is non-empty and holds only ASCII lowercase letters, digits, the
// runes in extra, and uppercase letters when upper is set.
func only(s, extra string, upper bool) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || upper && r >= 'A' && r <= 'Z'
		return !ok && !strings.ContainsRune(extra, r)
	})
}

// withoutID strips a trailing ` ^<id>` from s.
func withoutID(s, id string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "^"+id))
}

func (p *parser) addID(id string, line int) { p.ids = append(p.ids, idAt{id, line}) }

// actions records every task list item as a manual action.
func (p *parser) actions(doc ast.Node) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		li, ok := n.(*ast.ListItem)
		if !ok || !entering || li.FirstChild() == nil {
			return ast.WalkContinue, nil
		}
		first := li.FirstChild()
		box, ok := first.FirstChild().(*extast.TaskCheckBox)
		if !ok {
			return ast.WalkContinue, nil
		}
		line := p.line(li)
		// The checkbox parser matched `[ ]`, `[x]` or `[X]` at the start of the block.
		raw := p.linesText(first)
		txt := strings.TrimSpace(raw[strings.IndexByte(raw, ']')+1:])
		if lead, _, _ := strings.Cut(txt, " "); Icon(lead).isHeading() {
			p.errorf(line, "a manual action carries a checkbox, never a status icon")
		}
		mark := first.Lines().At(0).Start + 1 // inside the `[ ]` the block opens with
		a := ManualAction{Span: p.blockSpan(li), Head: p.span(p.nodeStart(li), p.nodeEnd(first)), Checked: box.IsChecked, Mark: mark, Text: txt}
		if id, ok, bad := p.trailingID(first); ok {
			p.placed[first] = true
			if bad != "" {
				p.errorf(line, "%s", bad)
			} else {
				a.ID, a.Text = id, withoutID(txt, id)
				p.addID(id, line)
			}
		}
		p.n.Actions = append(p.n.Actions, a)
		return ast.WalkContinue, nil
	})
}

// structure walks the top-level blocks, placing each section and the units in it.
func (p *parser) structure(doc ast.Node) {
	var prev *Item // the heading item the block before this one completed
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		if p.sec == secNone {
			if h, ok := c.(*ast.Heading); !ok || h.Level != 1 {
				if !p.beforeSeen {
					p.errorf(p.line(c), "content before the `# Worklog — <date>` title")
					p.beforeSeen = true
				}
				continue
			}
		}
		if p.pending != nil {
			if para, ok := c.(*ast.Paragraph); ok {
				prev = p.itemParagraph(para)
				continue
			}
			p.noItemID()
		}
		after := prev
		prev = nil
		switch c := c.(type) {
		case *ast.Heading:
			p.heading(c)
		case *ast.Paragraph:
			p.paragraph(c)
		case *ast.Blockquote:
			p.blockquote(c, after)
		case *ast.List:
			p.list(c)
		case *extast.Table:
			p.table(c)
		}
	}
	if p.pending != nil {
		p.noItemID()
	}
	p.closeSections(1, len(p.src))
	if p.sec == secNone && !p.beforeSeen {
		p.errorf(1, "no `# Worklog — <date>` title")
	}
}

func (p *parser) noItemID() {
	p.errorf(p.pending.Line, "a ##### work item needs a paragraph ending with ^<item-id> right under it")
	p.pending = nil
}

// open starts a section at a heading; closeSections ends it at the next heading at its level
// or above.
func (p *parser) open(level int, s *Span) { p.sections = append(p.sections, openSection{level, s}) }

func (p *parser) closeSections(level, at int) {
	for len(p.sections) > 0 && p.sections[len(p.sections)-1].level >= level {
		s := p.sections[len(p.sections)-1].span
		s.End = p.trimBack(at)
		p.sections = p.sections[:len(p.sections)-1]
	}
}

func (p *parser) heading(h *ast.Heading) {
	start := p.nodeStart(h)
	line := p.lineOf(start)
	txt := strings.TrimSpace(p.linesText(h))
	if h.Level == 1 {
		if p.sec != secNone {
			p.errorf(line, "a second `# ` title")
			return
		}
		p.sec = secPreamble
		p.n.Title.Span = p.blockSpan(h)
		date, ok := strings.CutPrefix(txt, "Worklog — ")
		if !ok {
			p.errorf(line, "the title must read `# Worklog — <YYYY-MM-DD>`")
			return
		}
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			p.errorf(line, "title date %q is not YYYY-MM-DD", date)
			return
		}
		p.n.Title.Date = date
		return
	}
	p.closeSections(h.Level, start)
	whole := Span{Start: start, Line: line}
	head := p.blockSpan(h)
	lead, rest, _ := strings.Cut(txt, " ")
	icon := Icon(lead)
	// goldmark's heading segment opens past the `#` run and its ASCII spaces; lead starts
	// past what TrimSpace also drops there, as an NBSP. An empty heading has no segment.
	var iconSpan Span
	if h.Lines().Len() > 0 {
		seg := h.Lines().At(0)
		raw := seg.Value(p.src)
		at := seg.Start + len(raw) - len(bytes.TrimLeftFunc(raw, unicode.IsSpace))
		iconSpan = p.span(at, at+len(lead))
	}
	switch h.Level {
	case 2:
		p.project, p.part, p.remains, p.bucket = nil, nil, nil, nil
		p.section2(txt, icon, iconSpan, rest, whole, head)
	case 3:
		p.part, p.remains, p.bucket = nil, nil, nil
		if p.sec != secProject {
			p.errorf(line, "a ### heading outside a project section")
			return
		}
		if !icon.isHeading() {
			p.errorf(line, "a ### heading must start with a status icon")
			return
		}
		if rest == "What remains" {
			if p.project.Remains != nil {
				p.errorf(line, "repeated `### What remains`")
				return
			}
			p.remains = &Remains{Span: whole, Heading: head, Icon: icon, IconSpan: iconSpan}
			p.project.Remains = p.remains
			p.open(3, &p.remains.Span)
			return
		}
		part, msg := parsePart(rest)
		if msg != "" {
			p.errorf(line, "%s", msg)
			return
		}
		part.Span, part.Heading, part.Icon = whole, head, icon
		p.part = part
		p.project.Parts = append(p.project.Parts, part)
		p.open(3, &part.Span)
	case 4:
		p.bucket = nil
		if p.remains == nil {
			p.errorf(line, "a #### bucket outside `### What remains`")
			return
		}
		if !icon.isHeading() {
			p.errorf(line, "a bucket heading must start with a status icon")
			return
		}
		if !slices.Contains(BucketNames, rest) {
			p.errorf(line, "unknown bucket %q", rest)
			return
		}
		for _, b := range p.remains.Buckets {
			if b.Name == rest {
				p.errorf(line, "repeated bucket %q", rest)
				return
			}
		}
		p.bucket = &Bucket{Span: whole, Heading: head, Icon: icon, IconSpan: iconSpan, Name: rest}
		p.remains.Buckets = append(p.remains.Buckets, p.bucket)
		p.open(4, &p.bucket.Span)
	case 5:
		if p.bucket == nil {
			p.errorf(line, "a ##### work item outside a bucket")
			return
		}
		if !icon.isState() {
			p.errorf(line, "a ##### work item must start with a status icon")
			return
		}
		p.pending = &Item{Span: head, Head: head, IconSpan: iconSpan, Icon: icon, Title: rest, Heading: true}
	default:
		p.errorf(line, "a level %d heading has no place in the note", h.Level)
	}
}

func (p *parser) section2(txt string, icon Icon, iconSpan Span, rest string, whole, head Span) {
	line := whole.Line
	fixed := map[string]bool{
		"Objectives": p.n.Objectives != nil, "Next": p.n.Next != nil,
		"Carry-over": p.n.CarryOver != nil, ProcessSection: p.n.Process != nil,
	}
	if seen, ok := fixed[txt]; ok && seen {
		p.errorf(line, "repeated section %q", txt)
		p.sec = secSkip
		return
	}
	switch txt {
	case "Objectives":
		p.n.Objectives = &Objectives{Span: whole}
		p.open(2, &p.n.Objectives.Span)
		p.sec = secObjectives
	case "Next":
		p.n.Next = &Next{Span: whole}
		p.open(2, &p.n.Next.Span)
		p.sec = secNext
	case "Carry-over":
		p.n.CarryOver = &CarryOver{Span: whole}
		p.open(2, &p.n.CarryOver.Span)
		p.sec = secCarryOver
	case ProcessSection:
		p.n.Process = &Process{Span: whole}
		p.open(2, &p.n.Process.Span)
		p.sec = secProcess
	default:
		p.sec = secSkip
		if !isRepo(rest) {
			p.errorf(line, "unknown section %q", txt)
			return
		}
		if !icon.isHeading() {
			p.errorf(line, "unknown status icon %q", icon)
			return
		}
		p.project = &Project{Span: whole, Heading: head, Icon: icon, IconSpan: iconSpan, Repo: rest}
		p.n.Projects = append(p.n.Projects, p.project)
		p.open(2, &p.project.Span)
		p.sec = secProject
	}
}

// ValidRepo reports whether s is a project's owner/repo.
func ValidRepo(s string) bool { return isRepo(s) }

func isRepo(s string) bool {
	owner, repo, ok := strings.Cut(s, "/")
	return ok && only(owner, "-_.", true) && only(repo, "-_.", true)
}

// parsePart reads `Part N — <kind>: <title>`; a `,` may stand for the `:`.
func parsePart(s string) (*Part, string) {
	const form = "a Part heading reads `Part N — <kind>: <title>`"
	after, ok := strings.CutPrefix(s, "Part ")
	if !ok {
		return nil, fmt.Sprintf("unknown ### heading %q: want `What remains` or `Part N — <kind>: <title>`", s)
	}
	num, tail, ok := strings.Cut(after, " — ")
	n, err := strconv.Atoi(num)
	if !ok || err != nil || n < 1 {
		return nil, form
	}
	for _, kind := range PartKinds {
		r, ok := strings.CutPrefix(tail, kind)
		if !ok {
			continue
		}
		if r == "" {
			return &Part{N: n, Kind: kind}, ""
		}
		if r[0] == ':' || r[0] == ',' {
			return &Part{N: n, Kind: kind, Title: strings.TrimSpace(r[1:])}, ""
		}
	}
	return nil, fmt.Sprintf("unknown Part kind in %q: want one of %s", tail, strings.Join(PartKinds, ", "))
}

// itemParagraph completes the pending ##### item with the paragraph under it.
func (p *parser) itemParagraph(para *ast.Paragraph) *Item {
	it := p.pending
	id, ok, bad := p.trailingID(para)
	if !ok {
		p.noItemID()
		p.paragraph(para)
		return nil
	}
	p.pending = nil
	p.placed[para] = true
	if bad != "" {
		p.errorf(p.line(para), "%s", bad)
		return nil
	}
	it.ID = id
	it.End = p.nodeEnd(para)
	it.Button = p.buttonLabel(para)
	p.dispatch(it, para)
	p.sent(it)
	p.addItem(it)
	p.bucket.Items = append(p.bucket.Items, it)
	return it
}

// buttonLabel is the label after the ▶ of a paragraph's button: a link to the Shell commands
// plugin reading `▶ <label>`. It is "" when the paragraph has none.
func (p *parser) buttonLabel(para *ast.Paragraph) string {
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		l, ok := c.(*ast.Link)
		if !ok || !bytes.HasPrefix(l.Destination, []byte(buttonURI)) {
			continue
		}
		if label, ok := strings.CutPrefix(p.inlineText(l), buttonMark); ok {
			return label
		}
	}
	return ""
}

// dispatch sets it's Dispatch from the first dispatch marker among block's inlines, and
// where its `^<id>` and that marker are. It returns the marker's source, "" without one.
func (p *parser) dispatch(it *Item, block ast.Node) string {
	last := block.Lines().At(block.Lines().Len() - 1)
	it.idAt = last.Start + bytes.LastIndex(last.Value(p.src), []byte("^"+it.ID))
	for c := block.FirstChild(); c != nil; c = c.NextSibling() {
		h, ok := c.(*ast.RawHTML)
		if !ok || h.Segments.Len() == 0 {
			continue
		}
		start, end := h.Segments.At(0).Start, h.Segments.At(h.Segments.Len()-1).Stop
		raw := string(p.src[start:end])
		if d, ok := parseMarker(raw); ok {
			it.Dispatch, it.marker = &d, Span{Start: start, End: end}
			return raw
		}
	}
	return ""
}

func (p *parser) addItem(it *Item) {
	p.addID(it.ID, it.Line)
	if _, dup := p.n.items[it.ID]; !dup {
		p.n.items[it.ID] = it
	}
}

func (p *parser) paragraph(para *ast.Paragraph) {
	if p.sec == secPreamble && !p.statusSeen {
		p.statusSeen = true
		p.status(para)
		return
	}
	if p.sec != secProject || (p.part == nil && p.bucket == nil) {
		return // prose; an ^id here is reported as unplaced
	}
	id, ok, bad := p.trailingID(para)
	if !ok {
		return
	}
	p.placed[para] = true
	line := p.line(para)
	if bad != "" {
		p.errorf(line, "%s", bad)
		return
	}
	p.n.Anchors = append(p.n.Anchors, Anchor{Span: p.blockSpan(para), ID: id})
	p.addID(id, line)
}

func (p *parser) status(para *ast.Paragraph) {
	const form = "the status line must start with **HH:MM UTC:** or **HH:MM UTC, <label>:**"
	line := p.line(para)
	strong, ok := para.FirstChild().(*ast.Emphasis)
	if !ok || strong.Level != 2 {
		p.errorf(line, "%s", form)
		return
	}
	s := p.inlineText(strong)
	if len(s) < 9 || s[2] != ':' || s[5:9] != " UTC" {
		p.errorf(line, "%s", form)
		return
	}
	if _, err := time.Parse(ClockLayout, s[:5]); err != nil {
		p.errorf(line, "%s", form)
		return
	}
	label := ""
	switch rest := s[9:]; {
	case rest == ":":
	case strings.HasPrefix(rest, ", ") && strings.HasSuffix(rest, ":") && len(rest) > 3:
		label = strings.TrimSpace(rest[2 : len(rest)-1])
	default:
		p.errorf(line, "%s", form)
		return
	}
	p.n.Status = &Status{Span: p.blockSpan(para), Time: s[:5], Label: label}
}

func (p *parser) isPromptCallout(q *ast.Blockquote) bool {
	para, ok := q.FirstChild().(*ast.Paragraph)
	if !ok || para.Lines().Len() == 0 {
		return false
	}
	first := para.Lines().At(0)
	return bytes.HasPrefix(first.Value(p.src), []byte("[!prompt]"))
}

func (p *parser) blockquote(q *ast.Blockquote, after *Item) {
	if !p.isPromptCallout(q) {
		return
	}
	line := p.line(q)
	var fences []*ast.FencedCodeBlock
	for c := q.FirstChild(); c != nil; c = c.NextSibling() {
		if f, ok := c.(*ast.FencedCodeBlock); ok && string(f.Language(p.src)) == "herdr-prompt" {
			fences = append(fences, f)
			p.placed[f] = true
		}
	}
	if after == nil {
		p.errorf(line, "a [!prompt] callout must follow its work item's ^<item-id> paragraph")
		return
	}
	if head := q.FirstChild().Lines().At(0); !bytes.HasPrefix(head.Value(p.src), []byte("[!prompt]-")) {
		p.errorf(line, "a prompt callout starts collapsed: `> [!prompt]-`")
		return
	}
	if len(fences) != 1 {
		p.errorf(line, "a [!prompt] callout holds exactly one herdr-prompt block, not %d", len(fences))
		return
	}
	f := fences[0]
	fline := p.line(f)
	attrs := map[string]string{}
	fields := strings.Fields(string(f.Info.Segment.Value(p.src)))[1:]
	for _, kv := range fields {
		k, v, ok := strings.Cut(kv, "=")
		if _, dup := attrs[k]; !ok || v == "" || dup || (k != "id" && k != "to") {
			p.errorf(fline, "unknown or repeated herdr-prompt attribute %q", kv)
			return
		}
		attrs[k] = v
	}
	if attrs["id"] == "" || attrs["to"] == "" {
		p.errorf(fline, "a herdr-prompt block needs id=<item-id> and to=<workspace label>")
		return
	}
	if attrs["id"] != after.ID {
		p.errorf(fline, "herdr-prompt id=%s under work item ^%s", attrs["id"], after.ID)
		return
	}
	pr := &Prompt{Span: p.blockSpan(q), ID: attrs["id"], To: attrs["to"], Body: p.linesText(f)}
	after.Prompt = pr
	after.End = pr.End
}

func (p *parser) list(l *ast.List) {
	switch p.sec {
	case secNext:
		line := p.line(l)
		if p.nextList {
			p.errorf(line, "## Next holds one list")
			return
		}
		p.nextList = true
		if !l.IsOrdered() {
			p.errorf(line, "the ## Next list must be ordered (1. …)")
			return
		}
		for li := l.FirstChild(); li != nil; li = li.NextSibling() {
			p.n.Next.Items = append(p.n.Next.Items, p.nextItem(li))
		}
	case secProcess:
		if p.n.Process.List != nil {
			p.errorf(p.line(l), "## Process holds one list")
			return
		}
		s := p.blockSpan(l)
		p.n.Process.List = &s
		for li := l.FirstChild(); li != nil; li = li.NextSibling() {
			first := li.FirstChild()
			if first == nil {
				continue
			}
			if _, box := first.FirstChild().(*extast.TaskCheckBox); box {
				continue // a manual action, recorded already
			}
			if txt := p.blockLine(first); txt != string(IconNone) {
				p.n.Process.Entries = append(p.n.Process.Entries, Entry{Span: p.blockSpan(li), Text: txt})
			}
		}
	case secProject:
		if p.bucket != nil {
			p.bucket.Lists = append(p.bucket.Lists, p.blockSpan(l))
		} else if p.part != nil {
			p.part.Lists = append(p.part.Lists, p.blockSpan(l))
		}
		p.projectList(l)
	}
}

func (p *parser) nextItem(li ast.Node) NextItem {
	it := NextItem{Span: p.blockSpan(li)}
	first := li.FirstChild()
	if first == nil {
		return it
	}
	if box, ok := first.FirstChild().(*extast.TaskCheckBox); ok {
		it.Box = Unchecked
		if box.IsChecked {
			it.Box = Checked
		}
	}
	// The details link is an Obsidian wikilink, plain text to goldmark: `[[#^<id>|details]]`.
	// Only the block's own text counts, never a code span or a link's label.
	var plain strings.Builder
	for c := first.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			plain.Write(t.Segment.Value(p.src))
		}
	}
	if _, ref, ok := strings.Cut(plain.String(), "[[#^"); ok {
		if end := strings.IndexAny(ref, "|]"); end >= 0 {
			if id := ref[:end]; ValidID(id) {
				it.Ref = id
			} else {
				p.errorf(it.Line, "invalid item id ^%s in a details link", id)
			}
		}
	}
	return it
}

// projectList places the work items of a list in a Part or bucket: items with a status icon
// and an ^id. Checkbox items are manual actions, recorded already.
func (p *parser) projectList(l *ast.List) {
	for li := l.FirstChild(); li != nil; li = li.NextSibling() {
		first := li.FirstChild()
		if first == nil {
			continue
		}
		if _, box := first.FirstChild().(*extast.TaskCheckBox); !box {
			p.listItem(li, first)
		}
		for c := first.NextSibling(); c != nil; c = c.NextSibling() {
			if sub, ok := c.(*ast.List); ok {
				p.projectList(sub)
			}
		}
	}
}

// entry records a bucket's list item with no id as an entry when a state icon leads it. `—`,
// which is not a state, prose, and a list item nested in a work item are not entries.
func (p *parser) entry(li, first ast.Node) {
	for a := li.Parent(); a != nil; a = a.Parent() {
		if anc, ok := a.(*ast.ListItem); ok && p.placed[anc.FirstChild()] {
			return // a step of a work item, part of its span
		}
	}
	lead, rest, _ := strings.Cut(p.blockLine(first), " ")
	if lead == string(IconNone) && rest == "" {
		p.bucket.Dashes = append(p.bucket.Dashes, p.blockSpan(li))
		return
	}
	if !Icon(lead).isState() {
		return
	}
	p.bucket.Entries = append(p.bucket.Entries, Entry{Span: p.blockSpan(li), Icon: Icon(lead), Text: strings.TrimSpace(rest)})
}

// blockLine is a block's source on one line: each line trimmed, joined by one space.
func (p *parser) blockLine(n ast.Node) string {
	parts := make([]string, n.Lines().Len())
	for i := range parts {
		seg := n.Lines().At(i)
		parts[i] = strings.TrimSpace(string(seg.Value(p.src)))
	}
	return strings.Join(parts, " ")
}

func (p *parser) listItem(li, first ast.Node) {
	id, ok, bad := p.trailingID(first)
	if !ok && p.part == nil && p.bucket != nil {
		p.entry(li, first)
		return
	}
	if !ok || (p.part == nil && p.bucket == nil) {
		return // no id, or one reported as unplaced
	}
	p.placed[first] = true
	line := p.line(li)
	if bad != "" {
		p.errorf(line, "%s", bad)
		return
	}
	lead, rest, _ := strings.Cut(p.blockLine(first), " ")
	if !Icon(lead).isState() {
		p.errorf(line, "work item ^%s must start with a status icon", id)
		return
	}
	icon := first.Lines().At(0).Start // a list item's text opens on its icon
	it := &Item{
		Span: p.blockSpan(li), Head: p.span(p.nodeStart(li), p.nodeEnd(first)), IconSpan: p.span(icon, icon+len(lead)),
		ID: id, Icon: Icon(lead), Title: withoutID(rest, id),
	}
	if raw := p.dispatch(it, first); raw != "" {
		it.Title = strings.Join(strings.Fields(strings.Replace(it.Title, raw, "", 1)), " ")
	}
	p.sent(it)
	p.addItem(it)
	if p.part != nil {
		p.part.Items = append(p.part.Items, it)
	} else {
		p.bucket.Items = append(p.bucket.Items, it)
	}
}

func (p *parser) table(t *extast.Table) {
	switch p.sec {
	case secObjectives:
		rows, ok := p.rows(t, &p.objTable, "Objectives", "Objective", "Done when", "Progress")
		if !ok {
			return
		}
		table := p.blockSpan(t)
		p.n.Objectives.Table = &table
		for _, r := range rows {
			lead, _, _ := strings.Cut(r.cells[2], " ")
			if !Icon(lead).isState() {
				p.errorf(r.span.Line, "an objective's progress must start with a status icon")
				continue
			}
			p.n.Objectives.Rows = append(p.n.Objectives.Rows, Objective{
				Span: r.span, Objective: r.cells[0], DoneWhen: r.cells[1], Progress: r.cells[2], Icon: Icon(lead),
			})
		}
	case secCarryOver:
		rows, ok := p.rows(t, &p.carryTable, "Carry-over", "Item", "State now", "Verdict")
		if !ok {
			return
		}
		table := p.blockSpan(t)
		p.n.CarryOver.Table = &table
		for _, r := range rows {
			p.n.CarryOver.Rows = append(p.n.CarryOver.Rows, CarryOverRow{
				Span: r.span, Item: r.cells[0], State: r.cells[1], Verdict: r.cells[2], Ref: p.blockRef(r.first),
			})
		}
	}
}

// blockRef is the block id an item cell's wikilink `[[<note>#^<id>|…]]` targets, as a
// carried row links its item; "" without a valid one. The wikilink is plain text to goldmark:
// only the cell's own text counts, never a code span or a link's label.
func (p *parser) blockRef(cell ast.Node) string {
	var plain strings.Builder
	for c := cell.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			plain.Write(t.Segment.Value(p.src))
		}
	}
	for rest := plain.String(); ; {
		_, link, ok := strings.Cut(rest, "[[")
		if !ok {
			return ""
		}
		target, after, ok := strings.Cut(link, "]]")
		if !ok {
			return ""
		}
		if end := strings.IndexAny(target, `|\`); end >= 0 {
			target = target[:end]
		}
		if _, id, ok := strings.Cut(target, "#^"); ok && ValidID(id) {
			return id
		}
		rest = after
	}
}

type row struct {
	span  Span
	cells []string
	first ast.Node // its first cell
}

// rows checks a section's one table against its header and returns its body rows.
func (p *parser) rows(t *extast.Table, seen *bool, section string, header ...string) ([]row, bool) {
	line := p.line(t)
	if *seen {
		p.errorf(line, "## %s holds one table", section)
		return nil, false
	}
	*seen = true
	var rows []row
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []string
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, strings.TrimSpace(p.linesText(c)))
		}
		if _, isHeader := r.(*extast.TableHeader); isHeader {
			if !slices.Equal(cells, header) {
				p.errorf(line, "the %s table header must be | %s |", section, strings.Join(header, " | "))
				return nil, false
			}
			continue
		}
		start := p.lineStart(r.Pos())
		rows = append(rows, row{span: p.span(start, p.lineEnd(start)), cells: cells, first: r.FirstChild()})
	}
	return rows, true
}

// unplaced reports every ^id and herdr-prompt block the structure walk did not place.
func (p *parser) unplaced(doc ast.Node) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || p.placed[n] {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			if id, ok, bad := p.trailingID(n); ok {
				if bad != "" {
					p.errorf(p.line(n), "%s", bad)
				} else {
					p.errorf(p.line(n), "^%s cannot be placed: an item id belongs to a work item, manual action or paragraph in a Part or bucket", id)
				}
			}
		case *ast.FencedCodeBlock:
			if string(n.Language(p.src)) == "herdr-prompt" {
				p.errorf(p.line(n), "a herdr-prompt block belongs in a `> [!prompt]-` callout under its work item")
			}
		}
		return ast.WalkContinue, nil
	})
}

func (p *parser) duplicates() {
	slices.SortStableFunc(p.ids, func(a, b idAt) int { return cmp.Compare(a.line, b.line) })
	first := map[string]int{}
	for _, x := range p.ids {
		if l, dup := first[x.id]; dup {
			p.errorf(x.line, "duplicate item id ^%s, first at line %d", x.id, l)
			continue
		}
		first[x.id] = x.line
	}
}

// nextRefs reports each *Next* details link whose id names no work item or anchor.
func (p *parser) nextRefs() {
	if p.n.Next == nil {
		return
	}
	for _, it := range p.n.Next.Items {
		if it.Ref != "" && !slices.ContainsFunc(p.ids, func(x idAt) bool { return x.id == it.Ref }) {
			p.errorf(it.Line, "details link ^%s names no work item or anchor", it.Ref)
		}
	}
}
