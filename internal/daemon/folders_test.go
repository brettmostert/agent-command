package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// A folder is a group with a path (PAN-188). Its path is made when it's set,
// and again when a session starts in it after it went missing; a session in
// it with no folder of its own starts there.
func TestFoldersMakeTheirPathAndStartSessionsThere(t *testing.T) {
	sessionsHere(t)
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p} })
	c := dial(t, p)
	path := filepath.Join(t.TempDir(), "work", "api")

	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "api", Path: path + "/"}, &g))
	if g.Path != path {
		t.Errorf("the folder's path is %q, want %q, clean", g.Path, path)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("the path wasn't made: %v", err)
	}

	// A session in it, with no folder given, starts at its path, which is made
	// again when it's gone.
	must(t, os.RemoveAll(path))
	var s ipc.Session
	must(t, c.Call(ipc.OpNew, ipc.NewRequest{HarnessKind: core.HarnessTerminal, GroupID: g.ID}, &s))
	if s.Cwd != path || s.GroupID != g.ID {
		t.Errorf("new in the folder: cwd %q, group %q", s.Cwd, s.GroupID)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the path wasn't made again: %v", err)
	}
	// One given its own folder keeps it.
	other := t.TempDir()
	must(t, c.Call(ipc.OpNew, ipc.NewRequest{HarnessKind: core.HarnessTerminal, GroupID: g.ID, Cwd: other}, &s))
	if s.Cwd != other {
		t.Errorf("a folder given was replaced: %q", s.Cwd)
	}

	// A group becomes a folder, and a folder a group again.
	var plain ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "plain"}, &plain))
	if plain.Path != "" {
		t.Errorf("a group has a path: %q", plain.Path)
	}
	made := filepath.Join(t.TempDir(), "new")
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupPath, GroupID: plain.ID, Path: made})
	if pathOf(t, c, plain.ID) != made {
		t.Error("group.path didn't make it a folder")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupPath, GroupID: plain.ID})
	if pathOf(t, c, plain.ID) != "" {
		t.Error("group.path with no path didn't make it a group again")
	}
	if _, err := os.Stat(made); err != nil {
		t.Error("clearing a path removed the folder on disk")
	}

	// A path that isn't absolute, or is a file, is refused, and nothing changes.
	file := filepath.Join(t.TempDir(), "f")
	must(t, os.WriteFile(file, nil, 0o600))
	for _, bad := range []string{"relative/path", file} {
		err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupPath, GroupID: plain.ID, Path: bad}, nil)
		if !hasCode(err, ipc.CodeInvalid) {
			t.Errorf("%q: want invalid, got %v", bad, err)
		}
		if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "x" + filepath.Base(bad), Path: bad}, nil); !hasCode(err, ipc.CodeInvalid) {
			t.Errorf("create at %q: want invalid, got %v", bad, err)
		}
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupPath, GroupID: "nope", Path: made}, nil); !hasCode(err, ipc.CodeNotFound) {
		t.Errorf("a missing group: %v", err)
	}
	if pathOf(t, c, plain.ID) != "" || len(snapshot(t, c).Groups) != 2 {
		t.Error("a refused path changed something")
	}

}

// ~/ is the home folder.
func TestAFoldersPathCanStartAtHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows' home
	if got, err := makeFolder("~/work/api"); err != nil || got != filepath.Join(home, "work", "api") {
		t.Errorf("~/: %q %v", got, err)
	}
}

func pathOf(t *testing.T, c *ipc.Conn, id string) string {
	t.Helper()
	for _, g := range snapshot(t, c).Groups {
		if g.ID == id {
			return g.Path
		}
	}
	t.Fatalf("no group %s", id)
	return ""
}
