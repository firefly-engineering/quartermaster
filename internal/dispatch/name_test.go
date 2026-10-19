package dispatch

import "testing"

// TestIsFreshAgent tells the names a fresh dispatch gives the agent in a workspace from the
// names an agent is renamed to, and from a session's, for a ref's workspace and a batch's.
func TestIsFreshAgent(t *testing.T) {
	t.Parallel()
	const ws = "/ws/o/b/issue-5"
	for _, name := range []string{"b-5", "b-5-2", "b-5-13"} {
		if !IsFreshAgent(name, ws) {
			t.Errorf("IsFreshAgent(%q, %s) is false", name, ws)
		}
	}
	for _, name := range []string{"", "-", "b", "b-5-", "b-5-1", "b-5-02", "b-5-x", "b-6", "bb-5", "b-5-2-2", "b-claude", "mine"} {
		if IsFreshAgent(name, ws) {
			t.Errorf("IsFreshAgent(%q, %s) is true", name, ws)
		}
	}
	if IsFreshAgent("b-5", "/ws/o/b/elsewhere") {
		t.Error("b-5 is the fresh agent of /ws/o/b/elsewhere")
	}
	// A batch's workspace is named after its item id, and so is its agent.
	const batch = "/ws/o/b/wave-2"
	for _, name := range []string{"b-wave-2", "b-wave-2-2"} {
		if !IsFreshAgent(name, batch) {
			t.Errorf("IsFreshAgent(%q, %s) is false", name, batch)
		}
	}
	for _, name := range []string{"b-5", "b-wave", "b-wave-2-1", "b-issue-wave-2"} {
		if IsFreshAgent(name, batch) {
			t.Errorf("IsFreshAgent(%q, %s) is true", name, batch)
		}
	}
}
