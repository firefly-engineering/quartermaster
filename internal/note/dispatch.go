package note

import (
	"strings"

	"github.com/yuin/goldmark/ast"
)

// Dispatch is where a work item was dispatched (spec §9.2): the herdr pane running its agent
// and the workspace it works in. The note carries it as an HTML comment on the item's line,
// `<!-- qm pane=<id> ws=<path> -->`, which Obsidian does not show.
type Dispatch struct{ Pane, Workspace string }

// The dispatch marker's comment: its opening, its tag, and its closing.
const (
	commentOpen  = "<!--"
	commentClose = "-->"
	markerTag    = "qm"
)

// DispatchMarker renders d as the comment a dispatched item carries on its line. Neither
// field may hold whitespace or `--`, or the marker would not read back.
func (r Renderer) DispatchMarker(d Dispatch) string {
	return commentOpen + " " + markerTag + " pane=" + d.Pane + " ws=" + d.Workspace + " " + commentClose
}

// dispatch reads the first dispatch marker among the inline nodes of block: a list item's
// first block or a ##### item's ^id paragraph. A comment in a code span is code, not a raw
// HTML node, so it is not a marker. A `qm` comment that does not read
// `pane=<id> ws=<path>`, once each and nothing else, is not one either.
func (p *parser) dispatch(block ast.Node) *Dispatch {
	for c := block.FirstChild(); c != nil; c = c.NextSibling() {
		raw, ok := c.(*ast.RawHTML)
		if !ok {
			continue
		}
		var b strings.Builder
		for i := range raw.Segments.Len() {
			seg := raw.Segments.At(i)
			b.Write(seg.Value(p.src))
		}
		if d := parseMarker(b.String()); d != nil {
			return d
		}
	}
	return nil
}

// parseMarker reads a raw HTML comment as a dispatch marker, or returns nil.
func parseMarker(s string) *Dispatch {
	body, ok := strings.CutPrefix(s, commentOpen)
	if !ok {
		return nil
	}
	if body, ok = strings.CutSuffix(body, commentClose); !ok {
		return nil
	}
	fields := strings.Fields(body)
	if len(fields) != 3 || fields[0] != markerTag {
		return nil
	}
	attrs := map[string]string{}
	for _, kv := range fields[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if _, dup := attrs[k]; !ok || v == "" || dup {
			return nil
		}
		attrs[k] = v
	}
	if attrs["pane"] == "" || attrs["ws"] == "" {
		return nil
	}
	return &Dispatch{Pane: attrs["pane"], Workspace: attrs["ws"]}
}
