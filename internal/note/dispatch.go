package note

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Dispatch is where task_dispatch started a fresh agent on a work item (spec §9.2): its herdr
// pane and its workspace. The item records it as an HTML comment, `<!-- qm pane=<id> ws=<path> -->`,
// just before its `^<id>`, in a list item's line or a ##### item's paragraph; Obsidian does not
// show it.
type Dispatch struct {
	Pane, Workspace string
}

// The marker's delimiters and keyword.
const (
	markerOpen  = "<!--"
	markerClose = "-->"
	markerWord  = "qm"
)

// DispatchMarker renders d as its HTML comment, `<!-- qm pane=<id> ws=<path> -->`.
func (r Renderer) DispatchMarker(d Dispatch) string {
	return markerOpen + " " + markerWord + " pane=" + d.Pane + " ws=" + d.Workspace + " " + markerClose
}

// Check reports why d cannot be written as a marker that parses back to d.
func (d Dispatch) Check() error {
	for _, f := range []struct{ name, v string }{{"pane", d.Pane}, {"ws", d.Workspace}} {
		if f.v == "" || strings.ContainsFunc(f.v, isSpace) || strings.Contains(f.v, "--") || strings.Contains(f.v, ">") {
			return fmt.Errorf("dispatch %s %q: want a non-empty value with no whitespace, -- or >", f.name, f.v)
		}
	}
	if !filepath.IsAbs(d.Workspace) {
		return fmt.Errorf("dispatch ws %q: want an absolute path", d.Workspace)
	}
	return nil
}

// parseMarker reads raw, an HTML comment's source, as a dispatch marker. ok is false for any
// other comment.
func parseMarker(raw string) (d Dispatch, ok bool) {
	body, ok := strings.CutPrefix(raw, markerOpen)
	if !ok {
		return Dispatch{}, false
	}
	if body, ok = strings.CutSuffix(body, markerClose); !ok {
		return Dispatch{}, false
	}
	f := strings.Fields(body)
	if len(f) != 3 || f[0] != markerWord {
		return Dispatch{}, false
	}
	for _, kv := range f[1:] {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "pane" && d.Pane == "":
			d.Pane = v
		case k == "ws" && d.Workspace == "":
			d.Workspace = v
		default:
			return Dispatch{}, false
		}
	}
	return d, d.Pane != "" && d.Workspace != ""
}

// SetDispatch records d on work item id of n, parsed from src, and returns the new source: it
// replaces the item's marker, or writes one just before its `^<id>`. Nothing else changes.
func SetDispatch(n *Note, src []byte, id string, d Dispatch) ([]byte, error) {
	if err := d.Check(); err != nil {
		return nil, fmt.Errorf("^%s: %w", id, err)
	}
	it := n.Item(id)
	if it == nil {
		return nil, fmt.Errorf("%w ^%s", ErrNoItem, id)
	}
	marker := Renderer{}.DispatchMarker(d)
	if it.Dispatch != nil {
		return Replace(src, it.marker, []byte(marker)), nil
	}
	return Insert(src, it.idAt, []byte(marker+" ")), nil
}
