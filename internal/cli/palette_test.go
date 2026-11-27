package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// PAN-190's CLI fixes: unarchive --as, and an
// archived group refused before anything is sent.
func TestTheCLIUnarchivesUnderANewNameAndRefusesArchivedGroups(t *testing.T) {
	f := newFakeDaemon(t)
	old := &core.Group{ID: "old", Name: "Backend", ArchivedAt: time.Unix(1, 0)}
	live := &core.Group{ID: "new", Name: "Backend"}
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Groups: ipc.GroupsOf([]*core.Group{old, live})})
	var muts []ipc.Mutation
	f.handle(ipc.OpMutate, func(raw json.RawMessage) (any, error) {
		var m ipc.Mutation
		must(t, json.Unmarshal(raw, &m))
		muts = append(muts, m)
		if m.Kind == ipc.MutGroupUnarchive && m.Text == "" {
			return nil, &ipc.RemoteError{Msg: "another group has that name", Code: ipc.CodeConflict}
		}
		return nil, nil
	})
	f.handle(ipc.OpNew, func(json.RawMessage) (any, error) {
		t.Error("a session was started in an archived group")
		return nil, nil
	})

	_, err := acmd(t, "group", "unarchive", "Backend")
	if err == nil || !strings.Contains(err.Error(), "Backend (new) has that name") || !strings.Contains(err.Error(), "--as 'Backend 2'") {
		t.Errorf("a taken name: %v", err)
	}
	mustRun(t, "group", "unarchive", "Backend", "--as", "Backend 2")
	if m := muts[len(muts)-1]; m.GroupID != "old" || m.Text != "Backend 2" {
		t.Errorf("unarchive --as: %+v", m)
	}

	mustRun(t, "group", "collapse", "Backend")
	if m := muts[len(muts)-1]; m.Kind != ipc.MutGroupCollapse || m.GroupID != "new" || !m.Flag || m.Host != "" {
		t.Errorf("collapse: %+v", m)
	}

	n := len(muts)
	_, err = acmd(t, "new", "--harness", "terminal", "--no-remote", "--group", "old")
	if err == nil || !strings.Contains(err.Error(), "Backend is archived: unarchive it first (acmd group unarchive Backend)") {
		t.Errorf("new --group <an archived group's id>: %v", err)
	}
	if len(muts) != n {
		t.Errorf("something was sent: %+v", muts[n:])
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"Backend": "Backend", "Backend 2": "'Backend 2'", "it's": `'it'\''s'`, "": "''"} {
		if got := shellQuote(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
