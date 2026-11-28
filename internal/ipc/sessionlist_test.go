package ipc

import (
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
)

// Every key that is set must match; a key with several values matches any
// of them. Archived sessions are left out unless asked for.
func TestSessionFilter(t *testing.T) {
	here := &core.Session{ID: "a", Status: core.StatusBusy, HarnessKind: core.HarnessClaude, HarnessName: "Work", GroupID: "g1"}
	studio := &core.Session{ID: "b", Host: "studio", Status: core.StatusWaiting, HarnessKind: core.HarnessTerminal}
	old := &core.Session{ID: "c", Status: core.StatusArchived, HarnessKind: core.HarnessAntigravity}
	all := []*core.Session{here, studio, old}
	for _, c := range []struct {
		name string
		f    SessionFilter
		want string
	}{
		{"none: everything not archived", SessionFilter{}, "ab"},
		{"archived too", SessionFilter{Archived: true}, "abc"},
		{"here", SessionFilter{On: []string{OnLocal}}, "a"},
		{"any host", SessionFilter{On: []string{OnRemote}}, "b"},
		{"a host by name", SessionFilter{On: []string{"studio"}}, "b"},
		{"another host", SessionFilter{On: []string{"mini"}}, ""},
		{"here or studio", SessionFilter{On: []string{OnLocal, "studio"}}, "ab"},
		{"a harness by name, ignoring case", SessionFilter{Harness: []string{" work "}}, "a"},
		{"no name: its kind's", SessionFilter{Harness: []string{"terminal", "antigravity"}, Archived: true}, "bc"},
		{"a group", SessionFilter{Group: []string{"g1"}}, "a"},
		{"statuses", SessionFilter{Status: []string{"waiting", "busy"}}, "ab"},
		{"all keys must match", SessionFilter{Status: []string{"busy"}, On: []string{"studio"}}, ""},
	} {
		got := ""
		for _, s := range all {
			if c.f.Match(s) {
				got += s.ID
			}
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if f := (SessionFilter{On: []string{"studio"}}); f.Here() || !f.OnHost("studio") || f.OnHost("mini") {
		t.Errorf("on studio: here %v, studio %v, mini %v", f.Here(), f.OnHost("studio"), f.OnHost("mini"))
	}
}
