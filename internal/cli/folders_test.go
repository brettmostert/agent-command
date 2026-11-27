package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Folders are groups with a path (PAN-188): acmd makes them, changes their
// path, shows their kind, and starts a session in one at its path.
func TestFoldersFromTheCLI(t *testing.T) {
	f := newFakeDaemon(t)
	groups := []*core.Group{{ID: "g1", Name: "api", Path: "/w/api"}, {ID: "g2", Name: "plain", SortKey: 1}}
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Groups: ipc.GroupsOf(groups)})
	var news []ipc.NewRequest
	f.handle(ipc.OpNew, func(raw json.RawMessage) (any, error) {
		var r ipc.NewRequest
		must(t, json.Unmarshal(raw, &r))
		news = append(news, r)
		return ipc.SessionOf(&core.Session{ID: "s", Name: "s", Cwd: r.Cwd}), nil
	})
	var muts []ipc.Mutation
	f.handle(ipc.OpMutate, func(raw json.RawMessage) (any, error) {
		var m ipc.Mutation
		must(t, json.Unmarshal(raw, &m))
		muts = append(muts, m)
		return ipc.GroupOf(&core.Group{ID: "g3", Name: m.Text, Path: m.Path}), nil
	})

	// In a folder with no --cwd, the daemon starts it at the folder's path;
	// in a group, it's the current folder, as before.
	mustRun(t, "new", "--group", "api", "--harness", "terminal")
	mustRun(t, "new", "--group", "plain", "--harness", "terminal")
	wd, _ := os.Getwd()
	if len(news) != 2 || news[0].Cwd != "" || news[0].GroupID != "g1" || news[1].Cwd != wd {
		t.Errorf("new: %+v", news)
	}

	out := mustRun(t, "group", "list")
	for _, want := range []string{"KIND", "PATH", "▤", "api", "/w/api", "●", "plain"} {
		if !strings.Contains(out, want) {
			t.Errorf("group list lacks %q:\n%s", want, out)
		}
	}

	// A folder's path is made absolute here; the daemon makes it if it's missing.
	out = mustRun(t, "group", "create", "web", "--folder", "work/web")
	if m := muts[len(muts)-1]; m.Kind != ipc.MutGroupCreate || m.Path != filepath.Join(wd, "work", "web") {
		t.Errorf("create --folder: %+v", m)
	}
	if !strings.Contains(out, `Created folder "web"`) {
		t.Errorf("create --folder said: %s", out)
	}
	mustRun(t, "group", "path", "plain", "~/notes")
	mustRun(t, "group", "path", "api", "--clear")
	if a, b := muts[len(muts)-2], muts[len(muts)-1]; a.Kind != ipc.MutGroupPath || a.GroupID != "g2" || a.Path != "~/notes" ||
		b.GroupID != "g1" || b.Path != "" {
		t.Errorf("group path: %+v %+v", a, b)
	}
	for _, bad := range [][]string{{"group", "path", "plain"}, {"group", "path", "plain", "/x", "--clear"}} {
		if _, err := acmd(t, bad...); err == nil {
			t.Errorf("%v should need a path or --clear", bad)
		}
	}
	if got := (&core.Group{}).KindMark() + (&core.Group{Path: "/p"}).KindMark(); got != "●▤" {
		t.Errorf("kind marks: %q", got)
	}
}
