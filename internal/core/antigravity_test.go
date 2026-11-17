package core

import (
	"strings"
	"testing"
	"time"
)

// agyHook is an agy payload with the common fields, plus extra ones.
func agyHook(t *testing.T, event, extra string) Event {
	t.Helper()
	raw := `{"conversationId":"conv-1","cwd":"/w","transcriptPath":"/w/.gemini/antigravity-cli/t.jsonl",` +
		`"hookEventName":"` + event + `","modelName":"auto","lastUserInput":"Fix the failing test"` + extra + `}`
	e, err := ParseHook(AntigravityHookPrefix+event, []byte(raw), at(1))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestParseAntigravityHook(t *testing.T) {
	anyEvent := func(Event) bool { return true }
	for _, tc := range []struct {
		event, extra string
		kind         EventKind
		check        func(Event) bool
	}{
		{"SessionStart", "", HookSessionStart, func(e Event) bool { return e.Source == "startup" && e.Prompt == "" }},
		{"PreInvocation", `,"invocationNum":1`, HookUserPromptSubmit, func(e Event) bool { return e.Prompt == "Fix the failing test" }},
		{"PostToolUse", `,"toolCall":{"id":"t1","name":"run_command","args":{"CommandLine":"go test"}},"error":"exit status 1"`, HookPostToolUse,
			func(e Event) bool { return e.ToolName == "run_command" && e.Prompt == "" }},
		{"Stop", `,"terminationReason":"NO_TOOL_CALL","fullyIdle":true`, HookStop, func(e Event) bool { return e.Error == "" }},
		{"Stop", `,"terminationReason":"USER_CANCELED"`, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"TERMINAL_STEP_TYPE"`, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"TERMINAL_CUSTOM_HOOK"`, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"INJECTED_RESPONSE"`, HookStop, anyEvent},
		{"Stop", ``, HookStop, anyEvent},
		{"Stop", `,"terminationReason":"ERROR","error":"quota exceeded\u001b[0m"`, HookStopFailure, func(e Event) bool { return e.Error == "quota exceeded[0m" }},
		{"Stop", `,"terminationReason":"ERROR","error":""`, HookStopFailure, func(e Event) bool { return e.Error == "an error" }},
		{"Stop", `,"terminationReason":"MAX_INVOCATIONS"`, HookStopFailure, func(e Event) bool { return e.Error == "the model call limit reached" }},
		{"Stop", `,"terminationReason":"MAX_FORCED_INVOCATIONS"`, HookStopFailure, func(e Event) bool { return e.Error == "the continuation limit reached" }},
		{"Stop", `,"terminationReason":"MAX_TOKEN_BUDGET_EXCEEDED"`, HookStopFailure, func(e Event) bool { return e.Error == "the token budget used up" }},
		{"Stop", `,"terminationReason":"MAX_SOMETHING_NEW"`, HookStopFailure, func(e Event) bool { return e.Error == "a limit reached (MAX_SOMETHING_NEW)" }},
		{"PostInvocation", "", EventKind("antigravity:PostInvocation"), anyEvent},
	} {
		e := agyHook(t, tc.event, tc.extra)
		if e.Kind != tc.kind || e.SessionID != "conv-1" || e.Cwd != "/w" || e.Agent != "Antigravity" || !e.Time.Equal(at(1)) ||
			e.TranscriptPath != "" || !tc.check(e) {
			t.Errorf("%s %s: %+v", tc.event, tc.extra, e)
		}
	}
}

func TestParseAntigravityHookName(t *testing.T) {
	// With no event after the prefix, the payload's name is used.
	e, err := ParseHook(AntigravityHookPrefix, []byte(`{"conversationId":"c","hookEventName":"Stop"}`), time.Time{})
	if err != nil || e.Kind != HookStop {
		t.Errorf("payload name: %v %v", e.Kind, err)
	}
	if !IsAntigravityHook("antigravity:Stop") || IsAntigravityHook("Stop") {
		t.Error("IsAntigravityHook")
	}
}

func TestParseAntigravityHookErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"invalid json":       `{`,
		"no conversation id": `{"hookEventName":"Stop","session_id":"claude-style"}`,
	} {
		if _, err := ParseHook("antigravity:Stop", []byte(raw), time.Time{}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// An agy session goes through the same states as a Claude one, without waiting.
func TestAntigravityLifecycle(t *testing.T) {
	s := &Session{ID: "s1", Harness: HarnessAntigravity, Kind: KindHosted, NameSource: NameAuto}
	if !s.IsAntigravity() || s.IsTerminal() {
		t.Fatal("harness checks")
	}
	Apply(s, Event{Kind: EventCreated, Time: at(0)})
	steps := []struct {
		event, extra string
		want         Status
	}{
		{"SessionStart", "", StatusDone},
		{"PreInvocation", "", StatusBusy},
		{"PostToolUse", `,"toolCall":{"name":"view_file"}`, StatusBusy},
		{"Stop", `,"terminationReason":"NO_TOOL_CALL"`, StatusDone},
		{"PreInvocation", "", StatusBusy},
		{"Stop", `,"terminationReason":"ERROR","error":"boom"`, StatusError},
	}
	for i, st := range steps {
		e := agyHook(t, st.event, st.extra)
		e.Time = at(i + 1)
		if !s.FollowConversation(e) {
			t.Fatalf("%s: dropped", st.event)
		}
		Apply(s, e)
		if s.Status != st.want {
			t.Fatalf("%s: status %s, want %s", st.event, s.Status, st.want)
		}
	}
	if s.ClaudeSessionID != "conv-1" || s.Cwd != "/w" || s.TranscriptPath != "" || s.StatusDetail != "boom" {
		t.Errorf("session: %+v", s)
	}
	if s.Name != NameFromPrompt("Fix the failing test") || s.Name == "" {
		t.Errorf("name %q", s.Name)
	}
	it, _ := InboxEffect(s, agyHook(t, "Stop", `,"terminationReason":"ERROR","error":"boom"`))
	if it == nil || it.Kind != InboxError || it.Text != "Antigravity stopped with boom." {
		t.Errorf("inbox: %+v", it)
	}
	if it, drop := InboxEffect(s, agyHook(t, "Stop", `,"terminationReason":"NO_TOOL_CALL"`)); it != nil || !drop {
		t.Errorf("done turn: %+v %v", it, drop)
	}
	// Claude's text is unchanged.
	it, _ = InboxEffect(s, Event{Kind: HookStopFailure, Error: "rate_limit"})
	if it == nil || !strings.HasPrefix(it.Text, "Claude stopped with rate_limit") {
		t.Errorf("claude inbox: %+v", it)
	}
}
