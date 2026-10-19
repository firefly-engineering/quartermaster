package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// script writes an executable shell script with body and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestNilEnvIsEmpty runs the child with an empty environment for a nil env, so it inherits
// none of this process's: HOME, set for every test run, is unset in the child.
func TestNilEnvIsEmpty(t *testing.T) {
	t.Parallel()
	out, err := Run(context.Background(), script(t, `printf '%s' "${HOME-unset}"`), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "unset" {
		t.Errorf("HOME = %q, want unset", out)
	}
}

// TestDirAndEnv runs the child in dir, with env as its whole environment.
func TestDirAndEnv(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), script(t, `printf '%s %s' "$(pwd -P)" "$QM"`), dir,
		[]string{"QM=set"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := dir + " set"; string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

// TestExitError is a non-zero exit: an *ExitError with the args and both streams, whose
// Message is standard error, else standard output, trimmed.
func TestExitError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, want string }{
		{"stderr", "echo out; echo ' err ' >&2; exit 1\n", "err"},
		{"stdout", "echo ' out '; exit 2\n", "out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Run(context.Background(), script(t, tc.body), "", nil, []string{"a", "b"})
			var exit *ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("err = %v, want an *ExitError", err)
			}
			if got := strings.Join(exit.Args, " "); got != "a b" {
				t.Errorf("Args = %q, want %q", got, "a b")
			}
			if got := exit.Message(); got != tc.want {
				t.Errorf("Message() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMissingPath is a run that never started: an error, and not an *ExitError. A bare name,
// as "sh", is never looked up on PATH.
func TestMissingPath(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "sh", filepath.Join(t.TempDir(), "absent")} {
		_, err := Run(context.Background(), path, "", nil, nil)
		var exit *ExitError
		if err == nil || errors.As(err, &exit) {
			t.Errorf("Run(%q) err = %v, want a start error", path, err)
		}
	}
}
