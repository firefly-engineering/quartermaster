package tools

import (
	"strings"
	"testing"
)

// TestTaskDispatchRefuses checks the input task_dispatch validates itself; internal/dispatch
// tests the dispatching.
func TestTaskDispatchRefuses(t *testing.T) {
	t.Parallel()
	d := fixtureDeps(t, settled(t), false)
	for _, c := range []struct {
		in   TaskDispatchInput
		want string
	}{
		{TaskDispatchInput{Ref: "#12"}, "owner/repo#N"},
		{TaskDispatchInput{Ref: "acme/web#12", Mode: "later"}, "mode"},
		{TaskDispatchInput{Ref: "acme/web#12", Date: "today"}, "not a date"},
	} {
		if _, err := d.taskDispatch(t.Context(), c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.in, err, c.want)
		}
	}
}
