package core

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AntigravityHookPrefix starts the event name of a hook that Antigravity's CLI
// (agy) runs, as in `acmd hook antigravity:Stop`. Its payloads aren't Claude's,
// and agy reads the hook's answer from stdout.
const AntigravityHookPrefix = "antigravity:"

// IsAntigravityHook reports whether name, an `acmd hook` event, comes from agy.
func IsAntigravityHook(name string) bool { return strings.HasPrefix(name, AntigravityHookPrefix) }

// antigravityAgent is how messages name agy.
const antigravityAgent = "Antigravity"

// agyPayload is the part of an agy hook's stdin acmd reads (camelCase, from
// protojson).
type agyPayload struct {
	ConversationID string `json:"conversationId"`
	HookEventName  string `json:"hookEventName"`
	Cwd            string `json:"cwd"`
	LastUserInput  string `json:"lastUserInput"`
	ToolCall       struct {
		Name string `json:"name"`
	} `json:"toolCall"`
	TerminationReason string          `json:"terminationReason"`
	Error             json.RawMessage `json:"error"`
}

// agyLimits are the Stop reasons for a turn cut short by a limit, said as
// "Antigravity stopped with …".
var agyLimits = map[string]string{
	"MAX_INVOCATIONS":           "the model call limit reached",
	"MAX_FORCED_INVOCATIONS":    "the continuation limit reached",
	"MAX_TOKEN_BUDGET_EXCEEDED": "the token budget used up",
}

// parseAntigravityHook turns an agy hook payload into Claude's vocabulary, so
// the state machine, the inbox and notifications treat both alike. agy has no
// event for a permission prompt, so its sessions are never "waiting".
//
// transcriptPath is left out on purpose: the transcript tail looks for Claude's
// interrupt marker, which an agy transcript can contain as ordinary text. agy
// sends Stop when the user cancels, so it doesn't need the tail.
func parseAntigravityHook(event string, raw []byte, at time.Time) (Event, error) {
	var p agyPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Event{}, fmt.Errorf("parse antigravity hook payload: %w", err)
	}
	if event == "" {
		event = p.HookEventName
	}
	if p.ConversationID == "" {
		return Event{}, errors.New("antigravity hook payload: no conversationId")
	}
	e := Event{
		Time:      at,
		SessionID: clean(p.ConversationID, 128),
		Cwd:       clean(p.Cwd, 4096),
		Agent:     antigravityAgent,
	}
	switch event {
	case "SessionStart":
		e.Kind, e.Source = HookSessionStart, "startup"
	case "PreInvocation":
		// Before each model call of a turn: like a submitted prompt, it means
		// busy, and the user's prompt can name the session.
		e.Kind, e.Prompt = HookUserPromptSubmit, clean(p.LastUserInput, 4096)
	case "PostToolUse":
		e.Kind, e.ToolName = HookPostToolUse, clean(p.ToolCall.Name, 128)
	case "Stop":
		reason := p.TerminationReason
		switch {
		case reason == "ERROR":
			e.Kind, e.Error = HookStopFailure, cmp.Or(clean(errorString(p.Error), 128), "an error")
		case agyLimits[reason] != "":
			e.Kind, e.Error = HookStopFailure, agyLimits[reason]
		case strings.HasPrefix(reason, "MAX_"): // a limit added since
			e.Kind, e.Error = HookStopFailure, "a limit reached ("+clean(reason, 64)+")"
		default:
			// NO_TOOL_CALL, USER_CANCELED, TERMINAL_*, INJECTED_RESPONSE: the
			// turn is over, and the user has the prompt back.
			e.Kind = HookStop
		}
	default:
		// Not one acmd registers; the state machine ignores it.
		e.Kind = EventKind(AntigravityHookPrefix + clean(event, 64))
	}
	return e, nil
}
