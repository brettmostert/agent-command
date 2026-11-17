package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/term"
)

// fakeAgent is the OSC 7501 report every host holds.
type fakeAgent struct {
	mu sync.Mutex
	st term.AgentState
}

func (f *fakeAgent) report(state, kind, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st = term.AgentState{State: state, Kind: kind, Msg: msg, Seq: f.st.Seq + 1, At: time.Now()}
}

func (f *fakeAgent) get(string) (term.AgentState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, true
}

// agy has no hook for waiting on you; what it reports on the terminal fills
// that in: blocked is waiting with its message, working is busy, failed an
// error, done the end of the turn.
func TestAntigravityStatusFromItsReports(t *testing.T) {
	p := testPaths(t)
	fh, fa := &fakeHosts{paths: p}, &fakeAgent{}
	start(t, p, func(c *Config) { c.Hosts = fh; c.FrontEvery = 20 * time.Millisecond; c.HostAgent = fa.get })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessAntigravity})
	get := func() *core.Session { return sessionByID(t, c, s.ID) }

	fa.report("working", "", "")
	waitFor(t, "busy", func() bool { return get().Status == core.StatusBusy })

	fa.report("blocked", "permission", "Allow `npm test`?")
	waitFor(t, "waiting", func() bool { return get().Status == core.StatusWaiting })
	if got := get().StatusDetail; got != "Allow `npm test`?" {
		t.Errorf("waiting detail %q", got)
	}

	fa.report("working", "", "")
	waitFor(t, "busy again once answered", func() bool { return get().Status == core.StatusBusy })

	fa.report("failed", "", "quota exceeded")
	waitFor(t, "error", func() bool { return get().Status == core.StatusError })
	if got := get().StatusDetail; got != "quota exceeded" {
		t.Errorf("error detail %q", got)
	}

	fa.report("working", "", "")
	waitFor(t, "busy", func() bool { return get().Status == core.StatusBusy })
	fa.report("done", "", "")
	waitFor(t, "done", func() bool { return get().Status == core.StatusDone })
}

func TestAgentStateEvent(t *testing.T) {
	at := time.Now()
	for _, c := range []struct {
		st   term.AgentState
		kind core.EventKind
		text string
	}{
		{term.AgentState{State: "working"}, core.HookPreToolUse, ""},
		{term.AgentState{State: "blocked", Kind: "question", Msg: "Which one?"}, core.HookNotification, "Which one?"},
		{term.AgentState{State: "blocked", Kind: "plan"}, core.HookNotification, "plan to review"},
		{term.AgentState{State: "blocked", Kind: "auth"}, core.HookNotification, "sign in"},
		{term.AgentState{State: "blocked"}, core.HookNotification, "waiting for you"},
		{term.AgentState{State: "idle"}, core.HookStop, ""},
		{term.AgentState{State: "done"}, core.HookStop, ""},
		{term.AgentState{State: "failed"}, core.HookStopFailure, "an error"},
	} {
		c.st.At = at
		e, ok := agentStateEvent(c.st)
		if !ok || e.Kind != c.kind || e.Time != at || e.Agent != "Antigravity" {
			t.Errorf("%+v: %+v %v", c.st, e, ok)
			continue
		}
		if got := e.Message + e.Error; got != c.text {
			t.Errorf("%+v: text %q, want %q", c.st, got, c.text)
		}
	}
	if _, ok := agentStateEvent(term.AgentState{State: "sleeping"}); ok {
		t.Error("an unknown state made an event")
	}
}

// agy's done report carries the reply. Its Stop hook has usually ended the turn
// already, with no text, yet the reply still becomes the Inbox's done item.
func TestAntigravityReplyReachesTheInbox(t *testing.T) {
	p := testPaths(t)
	fh, fa := &fakeHosts{paths: p}, &fakeAgent{}
	start(t, p, func(c *Config) { c.Hosts = fh; c.FrontEvery = 20 * time.Millisecond; c.HostAgent = fa.get })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessAntigravity})
	in := ipc.HookContext{HostSession: s.ID}
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("antigravity:PreInvocation", `{"conversationId":"c1","lastUserInput":"say ping"}`, t0, in), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("antigravity:Stop", `{"conversationId":"c1","terminationReason":"NO_TOOL_CALL"}`, t0.Add(time.Millisecond), in), nil))
	waitFor(t, "done from the hook", func() bool { return sessionByID(t, c, s.ID).Status == core.StatusDone })

	fa.report("done", "", "ping")
	waitFor(t, "the reply in the Inbox", func() bool {
		for _, it := range snapshot(t, c).Inbox {
			if it.SessionID == s.ID && it.Kind == core.InboxDone && it.Text == "ping" {
				return true
			}
		}
		return false
	})
}
