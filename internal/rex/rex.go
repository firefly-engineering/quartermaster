// Package rex drives Rex for send, refresh and dispatch, as package herdr drives herdr: it
// answers herdr.Mux's calls in herdr's types (ADR 0012). A herdr workspace is a rex session, a
// tab a window, a pane a block.
//
// Every rex call on a block or a window needs its session, so a pane id is
// `<session_id>/<block_id>` and a tab id `<session_id>/<window_id>`.
//
// Most calls go through `rex api call <method> <json>`, which answers JSON. Sessions and windows
// are made with `rex new` and `rex window new`, which take a cwd and a label where the raw API
// wants a block layout.
//
// Like every package but main, this one reads neither the environment nor the cwd: the caller
// gives the executable's path and its environment.
package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/command"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
)

// Runner runs rex with args and returns its standard output. A run that exits non-zero
// returns an *ExitError. Tests stand in a fake for it.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Client calls rex through Run. The zero Client reports rex as not available.
type Client struct {
	Run Runner
	// Tick is how often AgentStart looks for its program, and AgentPrompt for its prompt taken.
	// Zero is 250ms.
	Tick time.Duration
	// Patience and PromptPatience are how long AgentStart and AgentPrompt wait. Zero is 30s and
	// 15s.
	Patience, PromptPatience time.Duration
}

var _ herdr.Mux = Client{}

// New is a Client for the executable at path, run directly with no shell and with env as its
// whole environment. A path of "" is rex not found, which every call reports.
func New(path string, env []string) Client {
	return Client{Run: func(ctx context.Context, args ...string) ([]byte, error) {
		if path == "" {
			return nil, errors.New("rex not found on PATH")
		}
		out, err := command.Run(ctx, path, "", env, args)
		if exit := (*command.ExitError)(nil); errors.As(err, &exit) {
			return nil, (*ExitError)(exit)
		}
		if err != nil {
			return nil, fmt.Errorf("rex: %w", err)
		}
		return out, nil
	}}
}

// ExitError is a rex call that exited non-zero, with what it wrote to standard error.
type ExitError command.ExitError

func (e *ExitError) Error() string { return "rex " + verb(e.Args) + ": " + e.Message() }

// Message is what rex wrote to standard error, else to standard output, trimmed.
func (e *ExitError) Message() string { return (*command.ExitError)(e).Message() }

// code is the API error code in err, as not_found: rex's CLI ends a failed call's message with
// `(status=404 code=not_found)`. "" when err is not rex's or carries none.
//
// shortcut: rex is closed source, with no documented error shape on the CLI; read the token
// until it has one.
func code(err error) string {
	var exit *ExitError
	if !errors.As(err, &exit) {
		return ""
	}
	msg := exit.Message()
	i := strings.LastIndex(msg, "code=")
	if i < 0 {
		return ""
	}
	c := msg[i+len("code="):]
	if end := strings.IndexAny(c, ") \n"); end >= 0 {
		c = c[:end]
	}
	return c
}

// notFound is err, a call on something that does not exist, answering herdr's code want.
type notFound struct {
	err  error
	want string
}

func (e *notFound) Error() string { return e.err.Error() }
func (e *notFound) Unwrap() error { return e.err }
func (e *notFound) Code() string  { return e.want }

// gone wraps err in want when rex says its session or target does not exist.
func gone(err error, want string) error {
	if c := code(err); c == "not_found" || c == "session_not_found" {
		return &notFound{err: err, want: want}
	}
	return err
}

// join and split build and read a pane or tab id: a session id, then a block or window id.
func join(session, id string) string { return session + "/" + id }

func split(id string) (session, local string, err error) {
	session, local, ok := strings.Cut(id, "/")
	if !ok || session == "" || local == "" {
		return "", "", fmt.Errorf("rex id %q: want <session_id>/<id>", id)
	}
	return session, local, nil
}

// block is one of session.list_blocks's blocks.
type block struct {
	ID     string `json:"block_id"`
	Label  string `json:"label"`
	Window string `json:"window_id"`
}

// proc is one process in a block's `process` method's answer.
type proc struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

// WorkspaceList is `session.list`: each session's id and label.
func (c Client) WorkspaceList(ctx context.Context) ([]herdr.Workspace, error) {
	var r struct {
		Sessions []struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		} `json:"sessions"`
	}
	if err := c.call(ctx, &r, "session.list", struct{}{}); err != nil {
		return nil, err
	}
	ws := make([]herdr.Workspace, len(r.Sessions))
	for i, s := range r.Sessions {
		ws[i] = herdr.Workspace{ID: s.ID, Label: s.Label}
	}
	return ws, nil
}

// PaneList is every block of every session, as herdr's panes.
func (c Client) PaneList(ctx context.Context) ([]herdr.Pane, error) {
	spaces, err := c.WorkspaceList(ctx)
	if err != nil {
		return nil, err
	}
	var panes []herdr.Pane
	for _, s := range spaces {
		blocks, err := c.blocks(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range blocks {
			v, err := c.pane(ctx, s.ID, b)
			if err != nil {
				return nil, err
			}
			panes = append(panes, v.Pane)
		}
	}
	return panes, nil
}

// AgentList is every pane running a program other than its shell, named by its block's label.
func (c Client) AgentList(ctx context.Context) ([]herdr.Agent, error) {
	spaces, err := c.WorkspaceList(ctx)
	if err != nil {
		return nil, err
	}
	var agents []herdr.Agent
	for _, s := range spaces {
		blocks, err := c.blocks(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range blocks {
			p, err := c.pane(ctx, s.ID, b)
			if err != nil {
				return nil, err
			}
			if p.Kind != "" {
				agents = append(agents, herdr.Agent{Kind: p.Kind, Name: p.Label, Workspace: p.Workspace, Tab: p.Tab, Pane: p.ID, Cwd: p.Cwd, Status: p.Status})
			}
		}
	}
	return agents, nil
}

// PaneGet is the pane id names. A pane, or a session, that does not exist is an error whose
// herdr.Code is herdr.PaneNotFound.
func (c Client) PaneGet(ctx context.Context, id string) (herdr.Pane, error) {
	s, b, err := split(id)
	if err != nil {
		return herdr.Pane{}, err
	}
	v, err := c.look(ctx, s, b)
	return v.Pane, err
}

// WorkspaceCreate is `rex new <label> -c <cwd>`: a session with one shell. It returns that
// shell's pane. rex new shows the session in no app.
func (c Client) WorkspaceCreate(ctx context.Context, cwd, label string) (herdr.Pane, error) {
	var r struct {
		Session string `json:"session_id"`
		Windows []struct {
			ID     string   `json:"window_id"`
			Blocks []string `json:"block_ids"`
		} `json:"initial_windows"`
	}
	if err := c.json(ctx, &r, "new", label, "-c", cwd, "--json"); err != nil {
		return herdr.Pane{}, err
	}
	if r.Session == "" || len(r.Windows) == 0 || len(r.Windows[0].Blocks) == 0 {
		return herdr.Pane{}, errors.New("rex new: no block in its answer")
	}
	w := r.Windows[0]
	return herdr.Pane{ID: join(r.Session, w.Blocks[0]), Workspace: r.Session, Tab: join(r.Session, w.ID), Cwd: cwd}, nil
}

// TabCreate is `rex window new`, unfocused, in session workspace at cwd, labelled label. It
// returns the new window's shell pane.
func (c Client) TabCreate(ctx context.Context, workspace, cwd, label string) (string, error) {
	var r struct {
		Blocks []string `json:"block_ids"`
	}
	if err := c.json(ctx, &r, "window", "new", "-s", workspace, label, "-c", cwd, "--focus=false", "--json"); err != nil {
		return "", err
	}
	if len(r.Blocks) == 0 {
		return "", errors.New("rex window new: no block in its answer")
	}
	return join(workspace, r.Blocks[0]), nil
}

// TabClose is `session.close_window`: it closes the window and every block in it. A window
// already gone is an error whose herdr.Code is herdr.TabNotFound.
func (c Client) TabClose(ctx context.Context, id string) error {
	s, w, err := split(id)
	if err != nil {
		return err
	}
	err = c.call(ctx, nil, "session.close_window", map[string]string{"session_id": s, "window_id": w})
	return gone(err, herdr.TabNotFound)
}

// AgentStart labels pane's block name, then types kind and args into its shell and presses
// Enter: rex has no agent kinds, so kind is the program to run. It returns once the program is
// in the foreground and has reported a status. Not by Patience is an error, and nothing more is
// typed: a program that shows a screen before it reports, as claude's folder-trust question,
// would take a prompt's Enter as its answer.
func (c Client) AgentStart(ctx context.Context, name, kind, pane string, args []string) error {
	s, b, err := split(pane)
	if err != nil {
		return err
	}
	if err := c.call(ctx, nil, "session.set_block_label", map[string]string{"session_id": s, "block_id": b, "label": name}); err != nil {
		return err
	}
	if err := c.submit(ctx, s, b, shellLine(append([]string{kind}, args...))); err != nil {
		return err
	}
	tick, patience := c.tick(), c.Patience
	if patience == 0 {
		patience = 30 * time.Second
	}
	running := false
	for deadline := time.Now().Add(patience); ; {
		blocks, err := c.blocks(ctx, s)
		if err != nil {
			return err
		}
		for _, bl := range blocks {
			if bl.ID != b {
				continue
			}
			p, err := c.pane(ctx, s, bl)
			if err != nil {
				return err
			}
			if p.Kind != "" && p.Status != "" {
				return nil
			}
			running = p.Kind != ""
		}
		if time.Now().After(deadline) {
			if running {
				return fmt.Errorf("rex: %s in pane %s reported no status within %s: answer what its screen asks, as a folder-trust question, or check that it reports OSC 7501", kind, pane, patience)
			}
			return fmt.Errorf("rex: %s did not start in pane %s within %s", kind, pane, patience)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tick):
		}
	}
}

// AgentPrompt pastes text into pane's block and presses Enter: one turn, whatever its lines. It
// returns once the agent took it: it reports working or blocked, or a status rex recorded after
// the one seen before the paste, as a turn that finished between two looks leaves. Not by
// PromptPatience is an error quoting the screen's last lines: a screen that ate the prompt,
// as omp's setup wizard, reporting idle, shows there. The screen's words are never matched.
func (c Client) AgentPrompt(ctx context.Context, pane, text string) error {
	s, b, err := split(pane)
	if err != nil {
		return err
	}
	before, err := c.look(ctx, s, b)
	if err != nil {
		return err
	}
	if err := c.submit(ctx, s, b, text); err != nil {
		return err
	}
	tick, patience := c.tick(), c.PromptPatience
	if patience == 0 {
		patience = 15 * time.Second
	}
	for deadline := time.Now().Add(patience); ; {
		v, err := c.look(ctx, s, b)
		if err != nil {
			return err
		}
		if v.Status == herdr.Working || v.Status == herdr.Blocked || v.Reported != before.Reported {
			return nil
		}
		if time.Now().After(deadline) {
			screen, err := c.AgentRead(ctx, pane, notTakenLines)
			if err != nil {
				return fmt.Errorf("rex: prompt not taken by %s within %s; its screen could not be read: %w", pane, patience, err)
			}
			return fmt.Errorf("rex: prompt not taken by %s within %s; its screen ends:\n%s", pane, patience, strings.TrimSuffix(string(screen), "\n"))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tick):
		}
	}
}

// notTakenLines is how many of the screen's last lines a prompt not taken quotes.
const notTakenLines = 5

// tick is how often AgentStart and AgentPrompt look at their block.
func (c Client) tick() time.Duration {
	if c.Tick == 0 {
		return 250 * time.Millisecond
	}
	return c.Tick
}

// AgentRead is the last lines of pane's screen and scrollback, unwrapped, as plain text.
func (c Client) AgentRead(ctx context.Context, pane string, lines int) ([]byte, error) {
	s, b, err := split(pane)
	if err != nil {
		return nil, err
	}
	out, err := c.run(ctx, "capture", "-s", s, "-b", b, "--trim", "--unwrap")
	if err != nil {
		return nil, err
	}
	all := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	return []byte(strings.Join(all[max(0, len(all)-lines):], "\n") + "\n"), nil
}

// submit pastes text into a block, then presses Enter. A bracketed paste never submits on its
// own, in a shell or an agent's prompt.
func (c Client) submit(ctx context.Context, session, blk, text string) error {
	paste := map[string]any{"session_id": session, "block_id": blk, "event": map[string]any{"type": "paste", "payload": map[string]string{"text": text}}}
	if err := c.call(ctx, nil, "block.event", paste); err != nil {
		return err
	}
	_, err := c.run(ctx, "send-key", "-s", session, "-b", blk, "enter")
	return err
}

// blocks is `session.list_blocks` for session.
func (c Client) blocks(ctx context.Context, session string) ([]block, error) {
	var r struct {
		Blocks []block `json:"blocks"`
	}
	err := c.call(ctx, &r, "session.list_blocks", map[string]string{"session_id": session})
	return r.Blocks, err
}

// view is a block as herdr's pane, with its label and when its status was reported.
type view struct {
	herdr.Pane
	Label string
	// Reported is the updated_at of the record Status came from, as rex wrote it, compared only
	// for change: a report rex records later can only carry a later time, so a different one is
	// newer, and its format need not be known. "" when the program reported nothing.
	Reported string
}

// pane reads block b of session as herdr's pane. Its Kind is the name of the program in its
// foreground when that is not the block's shell, and its Status what that program last
// reported, "" when it reported nothing.
func (c Client) pane(ctx context.Context, session string, b block) (view, error) {
	v := view{Pane: herdr.Pane{ID: join(session, b.ID), Workspace: session, Tab: join(session, b.Window)}, Label: b.Label}
	var pr struct {
		Child      *proc  `json:"child"`
		Foreground *proc  `json:"foreground"`
		Ancestors  []proc `json:"ancestors"`
	}
	if err := c.method(ctx, &pr, session, b.ID, "process"); err != nil {
		return view{}, err
	}
	if pr.Foreground == nil {
		return v, nil
	}
	v.Cwd = pr.Foreground.Cwd
	if !agentRunning(pr.Child, pr.Foreground, pr.Ancestors) {
		return v, nil
	}
	v.Kind = pr.Foreground.Name
	var st struct {
		Records []struct {
			ID      string          `json:"id"`
			State   string          `json:"state"`
			Owner   *proc           `json:"owner"`
			Updated json.RawMessage `json:"updated_at"`
		} `json:"records"`
	}
	if err := c.method(ctx, &st, session, b.ID, "program_status"); err != nil {
		return view{}, err
	}
	for _, r := range st.Records {
		// The root record, reported by the program now in the foreground: an exited program's
		// record outlives it. pid and name together, as rex asks, since pids are reused.
		if r.ID == "" && r.Owner != nil && r.Owner.PID == pr.Foreground.PID && r.Owner.Name == pr.Foreground.Name {
			v.Status, v.Reported = r.State, string(r.Updated)
		}
	}
	return v, nil
}

// look is block blk of session, read as pane does. A block, or a session, that does not exist
// is an error whose herdr.Code is herdr.PaneNotFound.
func (c Client) look(ctx context.Context, session, blk string) (view, error) {
	blocks, err := c.blocks(ctx, session)
	if err != nil {
		return view{}, gone(err, herdr.PaneNotFound)
	}
	for _, b := range blocks {
		if b.ID == blk {
			v, err := c.pane(ctx, session, b)
			return v, gone(err, herdr.PaneNotFound)
		}
	}
	return view{}, &notFound{err: fmt.Errorf("rex: no block %s in session %s", blk, session), want: herdr.PaneNotFound}
}

// agentRunning reports whether fg, a block's foreground process, is a program other than the
// block's shell. ancestors runs from fg's parent up to child, the block's own process. The shell
// is child, or child's child when child is login, as macOS starts a login shell.
//
// shortcut: login is the one wrapper known; add any other a platform puts above the shell.
func agentRunning(child, fg *proc, ancestors []proc) bool {
	if child == nil || fg.PID == child.PID {
		return false
	}
	depth := 1
	if child.Name == "login" {
		depth = 2
	}
	return len(ancestors) >= depth
}

// shellLine is words as one line a POSIX shell or fish runs as that command: each word not
// plainly safe is single-quoted.
//
// shortcut: fish reads \\ and \' inside single quotes, so a word with a backslash breaks there;
// start the agent through a block command if a harness ever needs one.
func shellLine(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		if w != "" && !strings.ContainsFunc(w, func(r rune) bool { return !safe(r) }) {
			q[i] = w
			continue
		}
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

func safe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_=+:,./@%", r)
}

// method calls block method name on a block, decoding its answer into v.
func (c Client) method(ctx context.Context, v any, session, blk, name string) error {
	return c.call(ctx, v, "block.method", map[string]string{"session_id": session, "block_id": blk, "method": name})
}

// call is `rex api call <method> <args as JSON>`, its answer decoded into v unless v is nil.
func (c Client) call(ctx context.Context, v any, method string, args any) error {
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if v == nil {
		_, err = c.run(ctx, "api", "call", method, string(b))
		return err
	}
	return c.json(ctx, v, "api", "call", method, string(b))
}

// json runs rex with args and decodes its answer into v.
func (c Client) json(ctx context.Context, v any, args ...string) error {
	out, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("rex %s: %w", verb(args), err)
	}
	return nil
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.Run == nil {
		return nil, errors.New("rex: not available")
	}
	return c.Run(ctx, args...)
}

// verb is the call args make, as `api call session.list` or `window new`, which errors name it by.
func verb(args []string) string {
	if len(args) >= 3 && args[0] == "api" {
		return strings.Join(args[:3], " ")
	}
	return strings.Join(args[:min(2, len(args))], " ")
}
