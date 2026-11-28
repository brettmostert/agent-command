package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// acmd resume takes an agent's own session id, when no acmd session has it,
// and resumes it where session.list finds it: here, or on its host. A host
// that doesn't answer is named on stderr by list --resumable.
func TestResumeAnAgentSession(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpSnapshot, ipc.Snapshot{})
	var asked []ipc.SessionListRequest
	f.handle(ipc.OpSessionList, func(raw json.RawMessage) (any, error) {
		var r ipc.SessionListRequest
		must(t, json.Unmarshal(raw, &r))
		asked = append(asked, r)
		return ipc.SessionPage{Unreached: []string{"mini"}, Items: []*ipc.Session{
			{ClaudeSessionID: "aaaaaaaa-1111-4111-8111-111111111111", Name: "here work", HarnessID: "h1", HarnessKind: core.HarnessClaude, HarnessName: "Claude", Cwd: "/w/here"},
			{ClaudeSessionID: "bbbbbbbb-2222-4222-8222-222222222222", Name: "studio work", Host: "studio", HarnessID: "hw", HarnessKind: core.HarnessClaude, HarnessName: "Work", AgentLive: true},
			{ClaudeSessionID: "bbbbbbbb-2222-4222-8222-999999999999", Name: "twin", Host: "studio", HarnessKind: core.HarnessClaude},
		}}, nil
	})
	var news []ipc.NewRequest
	f.handle(ipc.OpNew, func(raw json.RawMessage) (any, error) {
		var r ipc.NewRequest
		must(t, json.Unmarshal(raw, &r))
		news = append(news, r)
		return ipc.SessionOf(&core.Session{ID: "s9", Name: "here work"}), nil
	})
	var remote []ipc.RemoteNewRequest
	f.handle(ipc.OpRemoteNew, func(raw json.RawMessage) (any, error) {
		var r ipc.RemoteNewRequest
		must(t, json.Unmarshal(raw, &r))
		remote = append(remote, r)
		return ipc.SessionOf(&core.Session{ID: "r9", Name: "studio work", Host: "studio"}), nil
	})

	out := mustRun(t, "resume", "aaaaaaaa")
	if len(news) != 1 || news[0].Resume != "aaaaaaaa-1111-4111-8111-111111111111" || news[0].HarnessID != "h1" || !strings.Contains(out, "Resumed") {
		t.Errorf("here: %+v\n%s", news, out)
	}
	if len(asked) == 0 || !asked[0].Filter.Resumable || !asked[0].Filter.Archived {
		t.Errorf("asked for %+v, want resumable and archived", asked)
	}
	out = mustRun(t, "resume", "bbbbbbbb-2222-4222-8222-222222222222")
	if len(remote) != 1 || remote[0].Host != "studio" || remote[0].HarnessID != "hw" || remote[0].Resume != "bbbbbbbb-2222-4222-8222-222222222222" ||
		!strings.Contains(out, "on studio") {
		t.Errorf("on studio: %+v\n%s", remote, out)
	}
	if _, err := acmd(t, "resume", "bbbbbbbb"); ExitCode(err) != exitInvalid {
		t.Errorf("a prefix two have: %v", err)
	}
	if _, err := acmd(t, "resume", "cccccccc"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("an id nothing has: %v", err)
	}

	var stdout, stderr bytes.Buffer
	root := NewRootCmd("acmd", &stdout, &stderr)
	root.SetArgs([]string{"list", "--resumable", "--on", "remote"})
	must(t, root.ExecuteContext(context.Background()))
	if !strings.Contains(stderr.String(), "mini didn't answer") {
		t.Errorf("stderr doesn't name mini: %q", stderr.String())
	}
	out = stdout.String()
	for _, want := range []string{"studio work", "Work", "(live)", "↗ studio", "-"} {
		if !strings.Contains(out, want) {
			t.Errorf("list --resumable is missing %q:\n%s", want, out)
		}
	}
	if last := asked[len(asked)-1]; len(last.Filter.On) != 1 || last.Filter.On[0] != ipc.OnRemote {
		t.Errorf("--on wasn't sent: %+v", last)
	}
}
