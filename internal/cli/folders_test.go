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
	mustRun(t, "new", "--group", "g1", "--harness", "terminal")
	mustRun(t, "new", "--group", "g2", "--harness", "terminal")
	wd, _ := os.Getwd()
	// A name never picks a group: --new-group makes one.
	if _, err := acmd(t, "new", "--group", "api", "--harness", "terminal"); err == nil || len(news) != 2 {
		t.Errorf("new --group <a name>: %v", err)
	}
	if _, err := acmd(t, "new", "--group", "g1", "--new-group", "x", "--harness", "terminal"); ExitCode(err) != exitUsage {
		t.Errorf("--group and --new-group: %v", err)
	}
	if len(news) != 2 || news[0].Cwd != "" || news[0].GroupID != "g1" || news[1].Cwd != wd {
		t.Errorf("new: %+v", news)
	}

	mustRun(t, "new", "--new-group", "fresh", "--harness", "terminal")
	if m := muts[len(muts)-1]; m.Kind != ipc.MutGroupCreate || m.Text != "fresh" || len(news) != 3 || news[2].GroupID != "g3" {
		t.Errorf("new --new-group: %+v %+v", m, news)
	}

	out := mustRun(t, "group", "list")
	for _, want := range []string{"ID", "g1", "KIND", "PATH", "▤", "api", "/w/api", "●", "plain"} {
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
	mustRun(t, "group", "path", "g2", "~/notes")
	mustRun(t, "group", "path", "g1", "--clear")
	if a, b := muts[len(muts)-2], muts[len(muts)-1]; a.Kind != ipc.MutGroupPath || a.GroupID != "g2" || a.Path != "~/notes" ||
		b.GroupID != "g1" || b.Path != "" {
		t.Errorf("group path: %+v %+v", a, b)
	}
	for _, bad := range [][]string{{"group", "path", "g2"}, {"group", "path", "g2", "/x", "--clear"}} {
		if _, err := acmd(t, bad...); err == nil {
			t.Errorf("%v should need a path or --clear", bad)
		}
	}
	if got := (&core.Group{}).KindMark() + (&core.Group{Path: "/p"}).KindMark(); got != "●▤" {
		t.Errorf("kind marks: %q", got)
	}
}

// acmd makes a group inside another, nests one, and lists children under
// their parent (PAN-189).
func TestNestedGroupsFromTheCLI(t *testing.T) {
	f := newFakeDaemon(t)
	groups := []*core.Group{{ID: "p", Name: "Backend"}, {ID: "o", Name: "Docs", SortKey: 1}, {ID: "c", Name: "Workers", ParentID: "p", SortKey: 2}}
	f.reply(ipc.OpSnapshot, ipc.Snapshot{Groups: ipc.GroupsOf(groups)})
	var muts []ipc.Mutation
	f.handle(ipc.OpMutate, func(raw json.RawMessage) (any, error) {
		var m ipc.Mutation
		must(t, json.Unmarshal(raw, &m))
		muts = append(muts, m)
		return ipc.GroupOf(&core.Group{ID: "n", Name: m.Text, ParentID: m.ParentID}), nil
	})
	out := mustRun(t, "group", "list")
	if !strings.Contains(out, "└ Workers") || strings.Index(out, "Workers") > strings.Index(out, "Docs") {
		t.Errorf("a child is listed under its parent:\n%s", out)
	}
	mustRun(t, "group", "create", "Queue", "--parent", "p")
	mustRun(t, "group", "nest", "o", "p")
	mustRun(t, "group", "nest", "c", "--top")
	if len(muts) != 3 || muts[0].ParentID != "p" || muts[1].Kind != ipc.MutGroupNest || muts[1].GroupID != "o" || muts[1].ParentID != "p" ||
		muts[2].GroupID != "c" || muts[2].ParentID != "" {
		t.Errorf("mutations: %+v", muts)
	}
	for _, bad := range [][]string{{"group", "nest", "o"}, {"group", "nest", "o", "p", "--top"}, {"group", "nest", "o", "nope"}, {"group", "nest", "Docs", "Backend"}} {
		if _, err := acmd(t, bad...); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}
