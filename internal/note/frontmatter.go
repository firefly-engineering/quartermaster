package note

import (
	"bytes"
	"fmt"
	"slices"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// projectsKey is the front matter key listing the note's projects.
const projectsKey = "projects"

// projectEntry renders repo as a line of the front matter's block projects list.
func projectEntry(repo, eol string) string { return "  - " + repo + eol }

// ListProject returns src with repo added to the `projects:` list of its front matter fm, a
// span from Parse of src, or of a source that differs from src only after fm. A repo listed
// already leaves src as it is, and so does a note with no front matter (fm nil): one not
// written from the template gains none.
//
// The list is spliced as written: a block list gains a line after its last item, with that
// item's indent and dash; a flow list gains `, <repo>` after its last item; an empty one, as
// `projects: []` or `projects:` with no value, becomes a block list; front matter with no
// projects key gains one before its closing fence. New lines take the opening fence's line
// end, LF or CRLF. A list it cannot splice that way, such as one whose last item is quoted or
// not a scalar, is an error, for the user to edit by hand.
func ListProject(src []byte, fm *Span, repo string) ([]byte, error) {
	if !isRepo(repo) {
		return nil, fmt.Errorf("project repo %q: want owner/repo", repo)
	}
	if fm == nil {
		return src, nil
	}
	open := lineEnd(src, fm.Start)
	eol := string(src[fm.Start:open][len(bytes.TrimRight(src[fm.Start:open], "\r\n")):])
	closing := bytes.LastIndexByte(src[:fm.End-1], '\n') + 1 // the closing fence's line start
	body := src[open:closing]
	newList := []byte(projectsKey + ":" + eol + projectEntry(repo, eol))

	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	if len(doc.Content) == 0 { // empty front matter
		return Insert(src, closing, newList), nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode || m.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("front matter at line %d: not a block mapping, `key: value` a line", fm.Line)
	}
	k := -1
	for i := 0; i < len(m.Content); i += 2 { // key, value, key, value…
		if m.Content[i].Value == projectsKey {
			k = i
			break
		}
	}
	if k < 0 {
		return Insert(src, closing, newList), nil
	}
	key, v := m.Content[k], m.Content[k+1]
	byHand := func(why string) error {
		return fmt.Errorf("the front matter's projects list, line %d, %s: add %s to it by hand", fm.Line+key.Line, why, repo)
	}
	marks := yamlMarks{body: body, lines: yamlLines(body)}
	at := func(n *yaml.Node) (int, error) { // n's offset in src
		off, ok := marks.offset(n.Line, n.Column)
		if !ok {
			return 0, byHand("is not where its parser placed it")
		}
		return open + off, nil
	}
	endOf := func(line int) int { return open + marks.lineEnd(line) }

	switch {
	case v.Kind == yaml.SequenceNode:
		if slices.ContainsFunc(v.Content, func(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Value == repo }) {
			return src, nil
		}
		if v.Style&yaml.FlowStyle != 0 && len(v.Content) == 0 {
			start, err := at(v)
			if err != nil {
				return nil, err
			}
			return emptyFlow(src, start, open+marks.lines[v.Line-1], repo, eol, byHand)
		}
		last := v.Content[len(v.Content)-1]
		start, err := at(last)
		if err != nil {
			return nil, err
		}
		if v.Style&yaml.FlowStyle != 0 {
			end := start + len(last.Value)
			if last.Kind != yaml.ScalarNode || last.Style != 0 || end > len(src) || string(src[start:end]) != last.Value {
				return nil, byHand("ends with an item written other than plain")
			}
			return Insert(src, end, []byte(", "+repo)), nil
		}
		prefix := src[open+marks.lines[last.Line-1] : start]
		// A scalar on one line holds its value there; one folded over lines does not.
		if last.Kind != yaml.ScalarNode || last.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 ||
			!bytes.Contains(src[start:endOf(last.Line)], []byte(last.Value)) || string(bytes.TrimSpace(prefix)) != "-" {
			return nil, byHand("ends with an item other than a one-line scalar")
		}
		return Insert(src, endOf(last.Line), []byte(string(prefix)+repo+eol)), nil
	case v.Kind == yaml.ScalarNode && v.Tag == "!!null" && v.Value == "":
		return Insert(src, endOf(key.Line), []byte(projectEntry(repo, eol))), nil
	}
	return nil, byHand("is not a list")
}

// emptyFlow turns an empty flow list, `[]` at offset at on the line from ls, into a block
// list holding repo, keeping what follows `]` on its line.
func emptyFlow(src []byte, at, ls int, repo, eol string, byHand func(string) error) ([]byte, error) {
	if src[at] != '[' {
		return nil, byHand("is not where its parser placed it")
	}
	end := at + 1
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	if end == len(src) || src[end] != ']' {
		return nil, byHand("spans lines")
	}
	start := at
	for start > ls && (src[start-1] == ' ' || src[start-1] == '\t') {
		start--
	}
	return Replace(src, Span{Start: start, End: end + 1}, []byte(eol+projectEntry(repo, ""))), nil
}

// yamlMarks turns the yaml parser's marks, a 1-based line and a 1-based column counted in
// characters, into byte offsets in body.
type yamlMarks struct {
	body  []byte
	lines []int // each line's start, by yaml's line breaks
}

// yamlLines is the start of each line of b as yaml counts lines: a line ends at CRLF, CR, LF,
// NEL, LS or PS (YAML 1.1 §4.1.4).
func yamlLines(b []byte) []int {
	starts := []int{0}
	for i := 0; i < len(b); {
		r, w := utf8.DecodeRune(b[i:])
		if r == '\r' && i+1 < len(b) && b[i+1] == '\n' {
			w = 2
		}
		i += w
		if (r == '\r' || r == '\n' || r == '\u0085' || r == ' ' || r == ' ') && i < len(b) {
			starts = append(starts, i)
		}
	}
	return starts
}

// offset is the byte offset of the mark at line, col; false when body has no such place.
func (m yamlMarks) offset(line, col int) (int, bool) {
	if line < 1 || line > len(m.lines) {
		return 0, false
	}
	off := m.lines[line-1]
	for range col - 1 {
		if off >= len(m.body) {
			return 0, false
		}
		_, w := utf8.DecodeRune(m.body[off:])
		off += w
	}
	return off, true
}

// lineEnd is the offset after line's end, its line break included.
func (m yamlMarks) lineEnd(line int) int {
	if line < len(m.lines) {
		return m.lines[line]
	}
	return len(m.body)
}

// lineEnd is the offset after the end of the line from off, its line end included.
func lineEnd(src []byte, off int) int {
	if i := bytes.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i + 1
	}
	return len(src)
}
