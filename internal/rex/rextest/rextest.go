// Package rextest is a fake rex for the tests of the packages that drive it: sessions of
// blocks, answered through rex's CLI as package rex calls it. One fake answers both in process,
// as a rex.Runner, and on disk, as the executable a test hands where a path to rex goes.
//
// The executable is a shell script that hands each call to the test process through a FIFO in
// the fake's directory, and prints the answer it gets back: a test binary started per call
// would cost a second under -race on macOS.
package rextest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/rex"
)

// script is the executable: it opens a FIFO of its own for the answer's exit status, sends its
// FIFO's path, its argument count and its arguments, each NUL-terminated, down the fake's
// request FIFO, then prints the answer the fake wrote beside its FIFO.
const script = `#!/bin/sh
d=%s
r="$d/answer.$$"
mkfifo "$r" || exit 3
exec 3<>"$r"
printf '%%s\0' "$r" "$#" "$@" > "$d/requests"
read -r code <&3
exec 3<&-
cat "$r.out"
cat "$r.err" >&2
rm -f "$r" "$r.out" "$r.err"
exit "$code"
`

// quit is the request that stops a fake's server.
const quit = "quit"

// State is everything the fake knows: the sessions, and how the test steers it.
type State struct {
	Sessions []Session
	// Fail makes a call fail, keyed by its name as Calls logs it, as session.list or window new,
	// or by start or prompt for a paste that starts an agent in a shell or prompts one. The value
	// is what the call writes to standard error; "" is "<key> failed".
	Fail map[string]string
	// OnStart is written when a paste starts an agent: a change the test makes while it starts.
	OnStart *File
	// Next numbers the next session `new` makes, as w<Next>, and the next window and block
	// `window new` makes, as t<Next> and p<Next>. Zero is 9.
	Next int
}

// File is a file's path and its text.
type File struct{ Path, Text string }

// Session is one rex session: a workspace.
type Session struct {
	ID, Label string
	Blocks    []Block
}

// Block is one block of a session, a pane, in its window, a tab.
type Block struct {
	ID, Window string
	Label      string // the agent's name; "" for none
	Cwd        string
	Kind       string // the program in its foreground; "" for its shell
	Status     string // what Kind last reported; "" for nothing
	Reports    int    // how many reports rex recorded: a paste into an agent is one more
	Screen     string // its screen and scrollback, as `capture` prints them
}

// Agent adds a, its pane and tab ids as the fake's (Pane), to its workspace's session, which is
// added unlabelled when there is none.
func (s *State) Agent(a rex.Agent) {
	s.Pane(rex.Pane{ID: a.Pane, Workspace: a.Workspace, Tab: a.Tab, Cwd: a.Cwd, Kind: a.Kind, Status: a.Status})
	sess, b := s.find(a.Pane)
	s.Sessions[sess].Blocks[b].Label = a.Name
}

// Pane adds block p, of id `<session>/<block>` in tab `<session>/<window>`, to its workspace's
// session, which is added unlabelled when there is none. A Kind with no Status is a program that
// reports nothing.
func (s *State) Pane(p rex.Pane) {
	_, blk, _ := strings.Cut(p.ID, "/")
	_, win, _ := strings.Cut(p.Tab, "/")
	b := Block{ID: blk, Window: win, Cwd: p.Cwd, Kind: p.Kind, Status: p.Status}
	if p.Status != "" {
		b.Reports = 1
	}
	i := s.session(p.Workspace)
	if i < 0 {
		s.Sessions = append(s.Sessions, Session{ID: p.Workspace})
		i = len(s.Sessions) - 1
	}
	s.Sessions[i].Blocks = append(s.Sessions[i].Blocks, b)
}

// Workspace adds a session of id, labelled label, or labels the one there is.
func (s *State) Workspace(id, label string) {
	if i := s.session(id); i >= 0 {
		s.Sessions[i].Label = label
		return
	}
	s.Sessions = append(s.Sessions, Session{ID: id, Label: label})
}

// Block is the block of pane id, nil when there is none.
func (s *State) Block(id string) *Block {
	sess, b := s.find(id)
	if b < 0 {
		return nil
	}
	return &s.Sessions[sess].Blocks[b]
}

func (s *State) session(id string) int {
	return slices.IndexFunc(s.Sessions, func(x Session) bool { return x.ID == id })
}

// find is the session and the block of pane id, -1 for each not found.
func (s *State) find(id string) (sess, blk int) {
	sid, bid, _ := strings.Cut(id, "/")
	if sess = s.session(sid); sess < 0 {
		return -1, -1
	}
	return sess, slices.IndexFunc(s.Sessions[sess].Blocks, func(b Block) bool { return b.ID == bid })
}

// Fake is a fake rex.
type Fake struct {
	Bin string // its executable, for where a path to rex goes

	mu    sync.Mutex
	state State
	calls []string
}

// New is a fake with no session, its executable written in a directory of its own and served
// until t ends.
func New(t *testing.T) *Fake {
	t.Helper()
	dir := t.TempDir()
	f := &Fake{Bin: filepath.Join(dir, "rex")}
	requests := filepath.Join(dir, "requests")
	if err := syscall.Mkfifo(requests, 0o600); err != nil {
		t.Fatal(err)
	}
	// Read and write, so opening it never blocks, and its reads never end while it is open.
	req, err := os.OpenFile(requests, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.serve(req)
	}()
	t.Cleanup(func() {
		_, _ = req.WriteString(quit + "\x00")
		<-done
		_ = req.Close()
	})
	// Hold ForkLock so no parallel fork inherits the write fd: golang/go#22315 (ETXTBSY).
	syscall.ForkLock.RLock()
	err = os.WriteFile(f.Bin, []byte(fmt.Sprintf(script, quote(dir))), 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// serve answers the executable's requests from req until it reads quit or req fails.
func (f *Fake) serve(req *os.File) {
	r := bufio.NewReader(req)
	token := func() (string, error) {
		s, err := r.ReadString(0)
		return strings.TrimSuffix(s, "\x00"), err
	}
	for {
		answer, err := token()
		if err != nil || answer == quit {
			return
		}
		count, err := token()
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(count)
		args := make([]string, n)
		for i := range args {
			if args[i], err = token(); err != nil {
				return
			}
		}
		out, err := f.call(args)
		code, stderr := 0, []byte(nil)
		if exit := (*rex.ExitError)(nil); errors.As(err, &exit) {
			code, stderr = 1, exit.Stderr
		} else if err != nil {
			code, stderr = 3, []byte(err.Error()+"\n")
		}
		_ = os.WriteFile(answer+".out", out, 0o600)
		_ = os.WriteFile(answer+".err", stderr, 0o600)
		// Not blocking: a script that is gone has no reader, and its answer is dropped.
		if w, err := os.OpenFile(answer, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_, _ = fmt.Fprintln(w, code)
			_ = w.Close()
		}
	}
}

// Client is a rex client on f in process, which looks at a block every millisecond.
func (f *Fake) Client() rex.Client { return rex.Client{Run: f.Run, Tick: time.Millisecond} }

// Run answers one rex call, as a rex.Runner.
func (f *Fake) Run(_ context.Context, args ...string) ([]byte, error) { return f.call(args) }

// Edit changes f's state through edit.
func (f *Fake) Edit(edit func(*State)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(&f.state)
}

// Calls are the calls made so far, in order, one line each but for a paste's text:
//
//	session.list
//	session.list_blocks <session>
//	process <pane>, program_status <pane>
//	label <pane> <name>
//	paste <pane> <text>
//	send-key <pane> <key>
//	capture <pane>
//	close <tab>
//	new <label> <cwd>
//	window new <session> <label> <cwd>
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Writes are Calls but the ones that only read, as one string, a newline after each.
func (f *Fake) Writes() string {
	var w strings.Builder
	for _, c := range f.Calls() {
		if name, _, _ := strings.Cut(c, " "); !slices.Contains(reads, name) {
			w.WriteString(c + "\n")
		}
	}
	return w.String()
}

// ClearCalls forgets the calls made so far.
func (f *Fake) ClearCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// reads are the calls that change nothing.
var reads = []string{"session.list", "session.list_blocks", "process", "program_status", "capture"}

// call answers args as rex would: its standard output, or a *rex.ExitError whose standard
// error ends as rex's CLI ends one, `(status=<n> code=<code>)`. Any other error is a call the
// fake does not know.
func (f *Fake) call(args []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &f.state
	c, err := parse(args)
	if err != nil {
		return nil, err
	}
	f.calls = append(f.calls, c.String())
	fail := func(key string) error {
		msg, ok := s.Fail[key]
		if !ok {
			return nil
		}
		if msg == "" {
			msg = key + " failed"
		}
		return &rex.ExitError{Args: args, Stderr: []byte(msg + "\n")}
	}
	if err := fail(c.name); err != nil {
		return nil, err
	}
	gone := func(code string) error {
		return &rex.ExitError{Args: args, Stderr: []byte("Error: " + c.name + " failed (status=404 code=" + code + ")\n")}
	}
	return answer(s, c, gone, fail)
}

// answer acts out c on s.
func answer(s *State, c call, gone func(string) error, fail func(string) error) ([]byte, error) {
	next := func() string {
		if s.Next == 0 {
			s.Next = 9
		}
		s.Next++
		return strconv.Itoa(s.Next - 1)
	}
	switch c.name {
	case "session.list":
		type session struct {
			ID    string `json:"session_id"`
			Label string `json:"label"`
		}
		r := struct {
			Sessions []session `json:"sessions"`
		}{Sessions: []session{}}
		for _, x := range s.Sessions {
			r.Sessions = append(r.Sessions, session{x.ID, x.Label})
		}
		return json.Marshal(r)
	case "new":
		id := "w" + next()
		s.Sessions = append(s.Sessions, Session{ID: id, Label: c.label, Blocks: []Block{{ID: "p1", Window: "t1", Cwd: c.cwd}}})
		return []byte(`{"session_id":"` + id + `","initial_windows":[{"window_id":"t1","block_ids":["p1"]}]}`), nil
	}
	i := s.session(c.session)
	if i < 0 {
		return nil, gone("session_not_found")
	}
	sess := &s.Sessions[i]
	switch c.name {
	case "session.list_blocks":
		type block struct {
			ID     string `json:"block_id"`
			Label  string `json:"label"`
			Window string `json:"window_id"`
		}
		r := struct {
			Blocks []block `json:"blocks"`
		}{Blocks: []block{}}
		for _, b := range sess.Blocks {
			r.Blocks = append(r.Blocks, block{b.ID, b.Label, b.Window})
		}
		return json.Marshal(r)
	case "window new":
		n := next()
		sess.Blocks = append(sess.Blocks, Block{ID: "p" + n, Window: "t" + n, Cwd: c.cwd})
		return []byte(`{"block_ids":["p` + n + `"]}`), nil
	case "close":
		kept := slices.DeleteFunc(slices.Clone(sess.Blocks), func(b Block) bool { return b.Window == c.target })
		if len(kept) == len(sess.Blocks) {
			return nil, gone("not_found")
		}
		sess.Blocks = kept
		return []byte(`{}`), nil
	}
	j := slices.IndexFunc(sess.Blocks, func(b Block) bool { return b.ID == c.target })
	if j < 0 {
		return nil, gone("not_found")
	}
	b := &sess.Blocks[j]
	switch c.name {
	case "process":
		if b.Kind == "" {
			return json.Marshal(map[string]any{"child": proc{10, "login", ""}, "foreground": proc{11, "sh", b.Cwd},
				"ancestors": []proc{{10, "login", ""}}})
		}
		return json.Marshal(map[string]any{"child": proc{20, "login", ""}, "foreground": proc{22, b.Kind, b.Cwd},
			"ancestors": []proc{{21, "sh", ""}, {20, "login", ""}}})
	case "program_status":
		if b.Status == "" {
			return []byte(`{"records":[]}`), nil
		}
		return json.Marshal(map[string]any{"records": []map[string]any{{"id": "", "state": b.Status,
			"owner": proc{22, b.Kind, ""}, "updated_at": "report " + strconv.Itoa(b.Reports)}}})
	case "label":
		b.Label = c.text
	case "paste":
		if b.Kind == "" {
			if err := fail("start"); err != nil {
				return nil, err
			}
			if fields := strings.Fields(c.text); len(fields) > 0 {
				b.Kind, b.Status, b.Reports = fields[0], rex.Idle, b.Reports+1
			}
			if s.OnStart != nil {
				if err := os.WriteFile(s.OnStart.Path, []byte(s.OnStart.Text), 0o644); err != nil {
					return nil, err
				}
			}
		} else {
			if err := fail("prompt"); err != nil {
				return nil, err
			}
			b.Reports++
		}
	case "capture":
		return []byte(b.Screen), nil
	}
	return []byte(`{}`), nil
}

type proc struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Cwd  string `json:"cwd,omitempty"`
}

// call is one rex call, as Calls logs it.
type call struct {
	name             string
	session, target  string // a block's or window's id, in session
	label, cwd, text string
}

func (c call) String() string {
	pane := c.session + "/" + c.target
	switch c.name {
	case "session.list":
		return c.name
	case "session.list_blocks":
		return c.name + " " + c.session
	case "label", "paste":
		return c.name + " " + pane + " " + c.text
	case "send-key":
		return c.name + " " + pane + " " + c.text
	case "new":
		return "new " + c.label + " " + c.cwd
	case "window new":
		return "window new " + c.session + " " + c.label + " " + c.cwd
	}
	return c.name + " " + pane
}

// parse reads args as one of the calls package rex makes.
func parse(args []string) (call, error) {
	bad := fmt.Errorf("rextest: unexpected rex %q", args)
	if len(args) == 0 {
		return call{}, bad
	}
	if args[0] == "api" {
		if len(args) != 4 || args[1] != "call" {
			return call{}, bad
		}
		var in struct {
			Session string `json:"session_id"`
			Block   string `json:"block_id"`
			Window  string `json:"window_id"`
			Method  string `json:"method"`
			Label   string `json:"label"`
			Event   struct {
				Type    string `json:"type"`
				Payload struct {
					Text string `json:"text"`
				} `json:"payload"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(args[3]), &in); err != nil {
			return call{}, fmt.Errorf("%w: %w", bad, err)
		}
		c := call{session: in.Session, target: in.Block}
		switch args[2] {
		case "session.list", "session.list_blocks":
			c.name = args[2]
		case "block.method":
			if in.Method != "process" && in.Method != "program_status" {
				return call{}, bad
			}
			c.name = in.Method
		case "session.set_block_label":
			c.name, c.text = "label", in.Label
		case "block.event":
			if in.Event.Type != "paste" {
				return call{}, bad
			}
			c.name, c.text = "paste", in.Event.Payload.Text
		case "session.close_window":
			c.name, c.target = "close", in.Window
		default:
			return call{}, bad
		}
		return c, nil
	}
	c := call{name: args[0]}
	rest := args[1:]
	if args[0] == "window" {
		if len(args) < 2 || args[1] != "new" {
			return call{}, bad
		}
		c.name, rest = "window new", args[2:]
	}
	var words []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; a {
		case "-s", "-b", "-c":
			if i+1 == len(rest) {
				return call{}, bad
			}
			i++
			switch a {
			case "-s":
				c.session = rest[i]
			case "-b":
				c.target = rest[i]
			case "-c":
				c.cwd = rest[i]
			}
		case "--json", "--focus=false", "--trim", "--unwrap":
		default:
			words = append(words, a)
		}
	}
	switch c.name {
	case "new", "window new":
		if len(words) != 1 {
			return call{}, bad
		}
		c.label = words[0]
	case "send-key":
		if len(words) != 1 {
			return call{}, bad
		}
		c.text = words[0]
	case "capture":
		if len(words) != 0 {
			return call{}, bad
		}
	default:
		return call{}, bad
	}
	return c, nil
}
