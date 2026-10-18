package server

import (
	"testing"
)

func TestCutPhases(t *testing.T) {
	t.Parallel()
	src := []byte("---\nname: x\n---\n\n# Skill\n\n## The day's cycle\n\nIntro.\n\n" +
		"### Open\n\nFirst.\n\n```\n### Not a heading\n```\n\n#### Detail\n\nKept.\n\n" +
		"### AFK\n\nSecond.\n\n## Routes\n\nAfter.\n")
	got, err := cutPhases(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"open": "### Open\n\nFirst.\n\n```\n### Not a heading\n```\n\n#### Detail\n\nKept.\n\n",
		"afk":  "### AFK\n\nSecond.\n\n",
	}
	if len(got) != len(want) {
		t.Fatalf("phases %v, want %v", keys(got), keys(want))
	}
	for name, text := range want {
		if got[name] != text {
			t.Errorf("%s: got %q, want %q", name, got[name], text)
		}
	}
}

func TestCutPhasesAtEOF(t *testing.T) {
	t.Parallel()
	got, err := cutPhases([]byte("## The day's cycle\n### Close\nLast."))
	if err != nil {
		t.Fatal(err)
	}
	if got["close"] != "### Close\nLast." {
		t.Fatalf("close: %q", got["close"])
	}
}

func TestCutPhasesNoCycle(t *testing.T) {
	t.Parallel()
	if _, err := cutPhases([]byte("# Skill\n\n### Open\n")); err == nil {
		t.Fatal("want an error when the cycle section is missing")
	}
}

func TestCutPhasesDuplicate(t *testing.T) {
	t.Parallel()
	if _, err := cutPhases([]byte("## The day's cycle\n### Open\n### open\n")); err == nil {
		t.Fatal("want an error when two phases share a name")
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
