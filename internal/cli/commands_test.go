package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/version"
)

// agents plays `claude agents --json`: the two background sessions are
// running with short ids, so archiving them must call claude stop.
type agents struct{}

func (agents) Agents(context.Context) ([]claudecli.Agent, error) {
	busy, waiting, pid := "busy", "waiting", 4242
	return []claudecli.Agent{
		{SessionID: "c-auth", ID: "aaaaaaaa", Kind: "background", Status: &busy, Pid: &pid, Cwd: "/w/api-server"},
		{SessionID: "c-flaky", ID: "ffffffff", Kind: "background", Status: &waiting, Pid: &pid, Cwd: "/w/web"},
	}, nil
}

// env starts a real daemon in temp dirs (via ACMD_* overrides) with three
// hosted sessions, whose hook events then come as Claude's would.
func env(t *testing.T) {
	t.Helper()
	// --host is kept between commands in one process: a test that calls a
	// command's code directly starts with none, whatever ran before it.
	origHost := targetHost
	targetHost = ""
	t.Cleanup(func() { targetHost = origHost })
	dir, err := os.MkdirTemp("", "acmdcli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r")}
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	orig := spawnDaemon
	spawnDaemon = func(paths.Paths) error { return errors.New("tests start the daemon themselves") }
	t.Cleanup(func() { spawnDaemon = orig })

	seedHosted(t, p, "c-auth", "c-flaky", "c-docs")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: version.Get().Version,
			Log: log.New(io.Discard, "", 0), Agents: agents{}, TailEvery: time.Hour, ReconcileEvery: 50 * time.Millisecond,
			Waker: envWaker})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})

	var c *ipc.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err = ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer c.Close()
	t0 := time.Now().Add(-time.Hour)
	send := func(name, payload string, offset time.Duration, hc ipc.HookContext) {
		t.Helper()
		if err := c.Call(ipc.OpEvent, ipc.HookEvent{Name: name, At: t0.Add(offset), Payload: json.RawMessage(payload), Context: hc}, nil); err != nil {
			t.Fatal(err)
		}
	}
	bg := ipc.HookContext{}
	send("SessionStart", `{"session_id":"c-auth","source":"startup","cwd":"/w/api-server"}`, 0, bg)
	send("UserPromptSubmit", `{"session_id":"c-auth","prompt":"refactor auth middleware"}`, time.Second, bg)
	send("SessionStart", `{"session_id":"c-flaky","source":"startup","cwd":"/w/web"}`, 2*time.Second, bg)
	send("UserPromptSubmit", `{"session_id":"c-flaky","prompt":"fix flaky e2e test"}`, 3*time.Second, bg)
	send("PermissionRequest", `{"session_id":"c-flaky","tool_name":"AskUserQuestion"}`, 4*time.Second, bg)
	send("SessionStart", `{"session_id":"c-docs","source":"startup","cwd":"/w/cli-tool"}`, 5*time.Second,
		ipc.HookContext{})
	send("UserPromptSubmit", `{"session_id":"c-docs","prompt":"docs: hooks reference"}`, 6*time.Second,
		ipc.HookContext{})
	send("Stop", `{"session_id":"c-docs"}`, 7*time.Second, ipc.HookContext{})
}

// seedHosted adds a hosted session for each Claude id, before the daemon
// starts, as `acmd new` does before its Claude's first hook.
func seedHosted(t *testing.T, p paths.Paths, claudeIDs ...string) {
	t.Helper()
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), p.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, cid := range claudeIDs {
		if err := st.CreateSession(context.Background(), &core.Session{Kind: core.KindHosted, ClaudeSessionID: cid, Status: core.StatusStarting}); err != nil {
			t.Fatal(err)
		}
	}
}

func acmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCmd("acmd", &out, &errOut)
	root.SetArgs(args)
	root.SetIn(strings.NewReader(""))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := acmd(t, args...)
	if err != nil {
		t.Fatalf("acmd %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// jsonRow is what tests read of list --json: the daemon's sessions.
type jsonRow = ipc.Session

// inGroup is the list as acmd list lays it out: each session's name and
// group.
type inGroup struct{ Name, Group string }

func listed(t *testing.T) []inGroup {
	t.Helper()
	var out []inGroup
	must(t, withClient(context.Background(), func(_ *client, snap snapshot) error {
		for _, r := range listRows(snap, core.ViewState{Mode: "groups", Sort: "created"}) {
			group := ""
			if r.Group != nil {
				group = r.Group.Name
			}
			out = append(out, inGroup{Name: r.Session.Name, Group: group})
		}
		return nil
	}))
	return out
}

// groupID is the id of this machine's group with name, as acmd group find
// prints it.
func groupID(t *testing.T, name string) string {
	t.Helper()
	var found []foundGroup
	must(t, json.Unmarshal([]byte(mustRun(t, "group", "find", name, "--json")), &found))
	return found[0].ID
}

func listJSON(t *testing.T, args ...string) []jsonRow {
	t.Helper()
	var rows []jsonRow
	if err := json.Unmarshal([]byte(mustRun(t, append([]string{"list", "--json"}, args...)...)), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func names(rows []jsonRow) string {
	var n []string
	for _, r := range rows {
		n = append(n, r.Name)
	}
	return strings.Join(n, " | ")
}

func TestListCommand(t *testing.T) {
	env(t)
	out := mustRun(t, "list")
	for _, want := range []string{"ID", "STATUS", "WHERE", "refactor auth middleware", "busy", "waiting", "api-server", "acmd"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	rows := listJSON(t)
	if names(rows) != "refactor auth middleware | fix flaky e2e test | docs: hooks reference" {
		t.Errorf("default order: %s", names(rows))
	}
	// Each line starts with the id commands take.
	if lines := strings.Split(out, "\n"); !strings.HasPrefix(lines[1], rows[0].ID+" ") {
		t.Errorf("the id isn't first:\n%s", out)
	}
	if rows[1].Status != "waiting" || !rows[1].Unseen {
		t.Errorf("rows: %+v", rows)
	}
	if got := names(listJSON(t, "--status", "waiting,busy")); got != "refactor auth middleware | fix flaky e2e test" {
		t.Errorf("--status: %s", got)
	}
	if got := names(listJSON(t, "--sort", "name")); got != "docs: hooks reference | fix flaky e2e test | refactor auth middleware" {
		t.Errorf("--sort name: %s", got)
	}
	if got := names(listJSON(t, "--sort", "name", "--desc")); !strings.HasPrefix(got, "refactor") {
		t.Errorf("--desc: %s", got)
	}
	if got := names(listJSON(t, "--filter", "flaky")); got != "fix flaky e2e test" {
		t.Errorf("--filter: %s", got)
	}
	if _, err := acmd(t, "list", "--group", "nope"); err == nil || !strings.Contains(err.Error(), "acmd group find") {
		t.Errorf("--group unknown: %v", err)
	}
	for _, bad := range [][]string{{"--sort", "bogus"}, {"--mode", "tree"}} {
		if _, err := acmd(t, append([]string{"list"}, bad...)...); ExitCode(err) != exitUsage {
			t.Errorf("list %v: %v", bad, err)
		}
	}
}

// Each filter is a flag, and --filter key=value is the same filter; --filter
// with no key (or an unknown one) is text. Different filters must all match,
// and one given twice matches either value.
func TestListFilters(t *testing.T) {
	env(t)
	same := func(a, b []string) {
		t.Helper()
		if x, y := names(listJSON(t, a...)), names(listJSON(t, b...)); x != y {
			t.Errorf("%v = %q, but %v = %q", a, x, b, y)
		}
	}
	same([]string{"--status", "waiting,busy"}, []string{"--filter", "status=waiting", "--filter", "status=busy"})
	same([]string{"--status", "waiting", "--status", "busy"}, []string{"--filter", "status=waiting,busy"})
	same([]string{"--on", "local"}, []string{"--filter", "on=local"})
	same([]string{"--filter", "flaky"}, []string{"--filter", "text=flaky"})
	if got := names(listJSON(t, "--on", "local")); got != "refactor auth middleware | fix flaky e2e test | docs: hooks reference" {
		t.Errorf("--on local: %s", got)
	}
	if got := names(listJSON(t, "--on", "remote")); got != "" {
		t.Errorf("--on remote with no hosts: %s", got)
	}
	if got := names(listJSON(t, "--status", "busy", "--filter", "flaky")); got != "" {
		t.Errorf("busy and flaky: %s, want none: both must match", got)
	}
	if got := names(listJSON(t, "--filter", "a=b")); got != "" {
		t.Errorf("an unknown key is text: %s", got)
	}
	if got := names(listJSON(t, "--harness", "Nope")); got != "" {
		t.Errorf("--harness Nope: %s", got)
	}
	if _, err := acmd(t, "list", "--on", "studio"); ExitCode(err) != exitNotFound || !strings.Contains(err.Error(), "acmd remote list") {
		t.Errorf("an unknown host: %v", err)
	}
	if _, err := acmd(t, "list", "--filter", "resumable=maybe"); ExitCode(err) != exitUsage {
		t.Errorf("resumable=maybe: %v", err)
	}
	if out := mustRun(t, "list"); !strings.Contains(out, "HARNESS") {
		t.Errorf("no harness column:\n%s", out)
	}

	// Resumable: only what's stopped, with its agent id.
	if out := mustRun(t, "list", "--resumable"); !strings.Contains(out, "Nothing to resume.") {
		t.Errorf("nothing stopped yet:\n%s", out)
	}
	docs := listJSON(t, "--filter", "docs")[0]
	mustRun(t, "finish", docs.ID, "--yes")
	out := mustRun(t, "list", "--filter", "resumable=true")
	for _, want := range []string{"AGENT ID", docs.ID, "c-docs", "finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("resumable is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "refactor auth middleware") {
		t.Errorf("a running session is resumable:\n%s", out)
	}
	if out := mustRun(t, "list", "--resumable", "--filter", "nothing-like-it"); !strings.Contains(out, "Nothing to resume.") {
		t.Errorf("text narrows resumable too:\n%s", out)
	}
	// An id nothing has is still not found.
	if _, err := acmd(t, "resume", "deadbeef-0000"); err == nil {
		t.Errorf("resume an unknown id: %v", err)
	}
}

// The view is the command's own, each time: nothing a list was given stays.
func TestListKeepsNoView(t *testing.T) {
	env(t)
	mustRun(t, "move", "c-auth", "--new-group", "api")
	if got := names(listJSON(t, "--mode", "list", "--sort", "name", "--desc")); !strings.HasPrefix(got, "refactor") {
		t.Errorf("list, by name, descending: %s", got)
	}
	if got := names(listJSON(t)); got != "refactor auth middleware | fix flaky e2e test | docs: hooks reference" {
		t.Errorf("the flags stayed: %s", got)
	}
	if out := mustRun(t, "list", "--mode", "list"); strings.Contains(out, "No group") {
		t.Errorf("list mode has no headers:\n%s", out)
	}
	for _, gone := range [][]string{{"view-mode", "list"}, {"sort", "name"}} {
		if _, err := acmd(t, gone...); err == nil {
			t.Errorf("acmd %v still runs", gone)
		}
	}
	for _, gone := range []string{"collapse", "expand"} {
		if _, err := acmd(t, "group", gone, "api"); ExitCode(err) != exitUsage || !strings.Contains(err.Error(), `unknown command "`+gone+`"`) {
			t.Errorf("acmd group %s: %v", gone, err)
		}
	}
	if out := mustRun(t, "group"); !strings.Contains(out, "Available Commands") {
		t.Errorf("acmd group on its own:\n%s", out)
	}
}

func TestRenameDescribeAndSelectors(t *testing.T) {
	env(t)
	mustRun(t, "rename", "c-flaky", "flaky", "e2e", "(PROJ-9)")
	rows := listJSON(t)
	mustRun(t, "describe", rows[0].ID, "PROJ-123 follow-up")
	rows = listJSON(t)
	if rows[1].Name != "flaky e2e (PROJ-9)" || rows[0].Description != "PROJ-123 follow-up" {
		t.Errorf("rows: %+v", rows)
	}
	// Only an id picks a session: never a number, a name or a prefix of one.
	for _, sel := range []string{"zzz", "1", "2", "refactor", "flaky e2e (PROJ-9)", rows[0].ID[:8]} {
		if _, err := acmd(t, "rename", sel, "x"); err == nil || !strings.Contains(err.Error(), "no session matches") ||
			!strings.Contains(err.Error(), "acmd find") || ExitCode(err) != exitFailed {
			t.Errorf("rename %q: %v", sel, err)
		}
	}
	if _, err := acmd(t, "rename", rows[0].ID, "   "); err == nil {
		t.Error("empty name should fail")
	}
}

// acmd find turns a name into ids: every match, one per line.
func TestFind(t *testing.T) {
	env(t)
	rows := listJSON(t)
	if out := mustRun(t, "find", "FLAKY"); out != rows[1].ID+"\n" {
		t.Errorf("find FLAKY = %q", out)
	}
	// Several: all of them, and it fails.
	out, err := acmd(t, "find", "e")
	if err == nil || !strings.Contains(err.Error(), "3 sessions match") || ExitCode(err) != exitFailed {
		t.Errorf("find e: %v", err)
	}
	if out != rows[0].ID+"\n"+rows[1].ID+"\n"+rows[2].ID+"\n" {
		t.Errorf("find e printed %q", out)
	}
	if out, err := acmd(t, "find", "nothing like it"); err == nil || out != "" || ExitCode(err) != exitFailed {
		t.Errorf("no match: %q %v", out, err)
	}

	mustRun(t, "move", "c-auth", "--new-group", "api")
	var found []foundSession
	must(t, json.Unmarshal([]byte(mustRun(t, "find", "auth", "--json")), &found))
	if len(found) != 1 || found[0].ID != rows[0].ID || found[0].Name != "refactor auth middleware" || found[0].Status != core.StatusBusy ||
		found[0].Group != groupID(t, "api") || found[0].Host != "" {
		t.Errorf("find --json: %+v", found)
	}
	out, err = acmd(t, "find", "zzz", "--json")
	if err == nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("find --json, none: %q %v", out, err)
	}
	// The id it prints is one commands take.
	mustRun(t, "rename", strings.TrimSpace(mustRun(t, "find", "docs")), "docs v2")
	if got := names(listJSON(t)); !strings.HasSuffix(got, "docs v2") {
		t.Errorf("rename by a found id: %s", got)
	}
}

// Several sessions at once: renamed with numbers, as the app does, and
// archived together.
func TestRenameAndArchiveSeveral(t *testing.T) {
	env(t)
	mustRun(t, "rename", "c-auth", "c-flaky", "--to", "api")
	rows := listJSON(t)
	names := []string{rows[0].Name, rows[1].Name}
	if strings.Join(names, ",") != "api - 1,api - 2" {
		t.Errorf("names = %v", names)
	}
	if _, err := acmd(t, "rename", "c-auth"); err == nil {
		t.Error("a rename with no name")
	}
	before := len(listJSON(t))
	mustRun(t, "archive", rows[0].ID, rows[1].ID, "--yes")
	if after := len(listJSON(t)); after != before-2 {
		t.Errorf("archived %d", before-after)
	}
	var out bytes.Buffer
	if err := reportResults(&out, ipc.Results{Results: []ipc.Result{{ID: "a", OK: true}, {ID: "b", Code: "not_found", Error: "gone"}}}, "Did %d.\n"); err == nil ||
		out.String() != "b: gone (not_found)\nDid 1.\n" {
		t.Errorf("report = %q, %v", out.String(), err)
	}
}

func TestGroupsAndMove(t *testing.T) {
	env(t)
	out := mustRun(t, "group", "create", "PROJ-123 auth")
	auth := groupID(t, "proj-123")
	if !strings.Contains(out, `Created group "PROJ-123 auth" (blue): `+auth) {
		t.Errorf("create: %s", out)
	}
	if _, err := acmd(t, "group", "create", "proj-123 AUTH"); err == nil {
		t.Error("duplicate group should fail")
	}
	mustRun(t, "move", "c-auth", "c-flaky", auth)
	// A group's name isn't its id: nothing is made, and nothing moves.
	if _, err := acmd(t, "move", "c-docs", "billing"); err == nil || !strings.Contains(err.Error(), "acmd group find") {
		t.Errorf("move to a name: %v", err)
	}
	if _, err := acmd(t, "move", "c-docs", "PROJ-123 auth"); err == nil {
		t.Error("moved into a group by its name")
	}
	if _, err := acmd(t, "move", "c-docs", "--new-group", "x", "--no-group"); ExitCode(err) != exitUsage {
		t.Errorf("--new-group and --no-group: %v", err)
	}
	if _, err := acmd(t, "move", "c-docs"); ExitCode(err) != exitUsage {
		t.Errorf("move with no group: %v", err)
	}
	mustRun(t, "move", "c-docs", "--new-group", "billing")
	billing := groupID(t, "billing")

	rows := listed(t)
	if rows[0].Group != "PROJ-123 auth" || rows[2].Group != "billing" {
		t.Errorf("groups: %+v", rows)
	}
	out = mustRun(t, "group", "list")
	if !strings.Contains(out, "PROJ-123 auth") || !strings.Contains(out, "billing") || strings.Contains(out, "Default") ||
		!strings.Contains(out, auth+"  ") || strings.Contains(out, "collapsed") {
		t.Errorf("group list:\n%s", out)
	}
	for _, args := range [][]string{{"group", "rename", "billing", "x"}, {"group", "color", "billing", "green"}, {"group", "delete", "billing", "--yes"}} {
		if _, err := acmd(t, args...); err == nil || !strings.Contains(err.Error(), "acmd group find") {
			t.Errorf("acmd %v by name: %v", args, err)
		}
	}
	mustRun(t, "group", "rename", billing, "billing v2")
	mustRun(t, "group", "color", billing, "green")
	if out := mustRun(t, "group", "list"); !strings.Contains(out, "billing v2") || !strings.Contains(out, "green") {
		t.Errorf("rename/color:\n%s", out)
	}
	if got := names(listJSON(t, "--group", billing)); got != "docs: hooks reference" {
		t.Errorf("--group: %s", got)
	}

	// Deleting a group archives its sessions.
	if _, err := acmd(t, "group", "delete", billing); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("delete without confirmation: %v", err)
	}
	mustRun(t, "group", "delete", billing, "--yes")
	if got := names(listJSON(t)); strings.Contains(got, "docs") {
		t.Errorf("after delete: %s", got)
	}
}

// acmd group find: every group whose name has the text, archived ones too.
func TestGroupFind(t *testing.T) {
	env(t)
	mustRun(t, "group", "create", "api")
	mustRun(t, "group", "create", "web api")
	mustRun(t, "group", "create", "docs")
	api, docs := groupID(t, "web"), groupID(t, "docs")
	out, err := acmd(t, "group", "find", "API")
	if err == nil || !strings.Contains(err.Error(), "2 groups match") || strings.Count(out, "\n") != 2 || !strings.Contains(out, api) {
		t.Errorf("find API: %q %v", out, err)
	}
	mustRun(t, "group", "archive", docs, "--yes")
	var found []foundGroup
	must(t, json.Unmarshal([]byte(mustRun(t, "group", "find", "doc", "--json")), &found))
	if len(found) != 1 || found[0].ID != docs || !found[0].Archived {
		t.Errorf("an archived group: %+v", found)
	}
	if out, err := acmd(t, "group", "find", "zzz", "--json"); err == nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("none: %q %v", out, err)
	}
}

func TestArchive(t *testing.T) {
	env(t)
	if _, err := acmd(t, "archive", "c-auth"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("archive without confirmation: %v", err)
	}
	mustRun(t, "archive", "c-docs", "--yes")
	if got := names(listJSON(t)); strings.Contains(got, "docs") {
		t.Errorf("archived still listed: %s", got)
	}
	if got := names(listJSON(t, "--archived")); !strings.Contains(got, "docs") {
		t.Errorf("--archived should show archived: %s", got)
	}
	mustRun(t, "unarchive", "c-docs")
	for _, r := range listJSON(t) {
		if r.Name == "docs: hooks reference" && r.Status != core.StatusPaused {
			t.Errorf("unarchived status: %s", r.Status)
		}
	}

	// Group archive archives its sessions.
	mustRun(t, "move", "c-auth", "c-flaky", "--new-group", "PROJ-1")
	proj := groupID(t, "PROJ-1")
	mustRun(t, "group", "archive", proj, "--yes")
	if got := names(listJSON(t)); got != "docs: hooks reference" {
		t.Errorf("after group archive: %s", got)
	}
	if out := mustRun(t, "group", "list", "--archived"); !strings.Contains(out, "archived") {
		t.Errorf("group list --archived:\n%s", out)
	}
	// Nothing goes into an archived group.
	if _, err := acmd(t, "move", "c-docs", proj); err == nil || !strings.Contains(err.Error(), "unarchive it first (acmd group unarchive "+proj+")") {
		t.Errorf("move into an archived group: %v", err)
	}
	if _, err := acmd(t, "group", "unarchive", "PROJ-1"); err == nil {
		t.Error("unarchived a group by its name")
	}
	mustRun(t, "group", "unarchive", proj, "--sessions")
	if got := len(listJSON(t)); got != 3 {
		t.Errorf("after group unarchive --sessions: %d sessions", got)
	}
	if _, err := acmd(t, "group", "unarchive", proj); err == nil || !strings.Contains(err.Error(), "isn't archived") {
		t.Errorf("unarchive of a live group: %v", err)
	}
}

func TestConfirmAndHelpers(t *testing.T) {
	if err := confirm(strings.NewReader(""), io.Discard, true, "q"); err != nil {
		t.Errorf("--yes: %v", err)
	}
	if err := confirm(strings.NewReader("y\n"), io.Discard, false, "q"); err == nil {
		t.Error("non-terminal input must require --yes")
	}
	now = func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	base := now()
	for d, want := range map[time.Duration]string{5 * time.Second: "5s", 3 * time.Minute: "3m", 2 * time.Hour: "2h", 50 * time.Hour: "2d"} {
		if got := age(&core.Session{LastEventAt: base.Add(-d)}); got != want {
			t.Errorf("age(%v) = %s, want %s", d, got, want)
		}
	}
	if age(&core.Session{}) != "-" || age(&core.Session{CreatedAt: base.Add(-time.Minute)}) != "1m" {
		t.Error("age fallbacks")
	}
	if display("", 10) != "(unnamed)" || display("abcdefghijkl", 5) != "abcd…" {
		t.Error("display")
	}
	if where(&core.Session{Kind: core.KindHosted}) != "acmd" {
		t.Error("where here")
	}
	var buf bytes.Buffer
	if err := writeListTable(&buf, nil); err != nil || !strings.Contains(buf.String(), "No sessions") {
		t.Errorf("empty table: %q %v", buf.String(), err)
	}
}

func TestPurge(t *testing.T) {
	env(t)
	if out := mustRun(t, "purge", "--yes"); !strings.Contains(out, "No archived sessions to purge.") {
		t.Errorf("nothing archived:\n%s", out)
	}
	mustRun(t, "archive", "c-docs", "--yes")
	if out := mustRun(t, "purge", "--older-than", "30d", "--yes"); !strings.Contains(out, "No archived sessions") {
		t.Errorf("recently archived purged:\n%s", out)
	}
	if _, err := acmd(t, "purge"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("purge without confirmation: %v", err)
	}
	if out := mustRun(t, "purge", "--yes"); !strings.Contains(out, "Purged 1 session(s).") {
		t.Errorf("purge:\n%s", out)
	}
	if rows := listJSON(t, "--archived"); len(rows) != 2 {
		t.Errorf("%d sessions left", len(rows))
	}
	for _, bad := range []string{"x", "-1d", "-5h"} {
		if _, err := acmd(t, "purge", "--older-than", bad, "--yes"); err == nil {
			t.Errorf("--older-than %s accepted", bad)
		}
	}
	if d, err := parseAge("12h"); err != nil || d != 12*time.Hour {
		t.Errorf("12h: %v %v", d, err)
	}
}

func TestScreenCommand(t *testing.T) {
	env(t)
	if _, err := acmd(t, "screen", "zzz"); err == nil || !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("unknown: %v", err)
	}
	// The test sessions have no terminal running: it says so, rather than printing nothing.
	if _, err := acmd(t, "screen", "c-auth", "--lines", "5"); err == nil || !strings.Contains(err.Error(), "isn't running") {
		t.Errorf("no terminal: %v", err)
	}
	if _, err := acmd(t, "screen", "c-auth", "--json"); err == nil {
		t.Error("json for no terminal")
	}
}

// acmd paths says where things are, after the environment, for UIs and scripts.
func TestPathsCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ACMD_STATE_DIR", filepath.Join(dir, "s"))
	t.Setenv("ACMD_RUNTIME_DIR", filepath.Join(dir, "r"))
	t.Setenv("ACMD_CONFIG_DIR", filepath.Join(dir, "c"))
	out := mustRun(t, "paths", "--json")
	var got pathsJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Socket != filepath.Join(dir, "r", "daemon.sock") || got.Config != filepath.Join(dir, "c", "config.toml") ||
		got.DB != filepath.Join(dir, "s", "state.db") || got.IdentityDir != filepath.Join(dir, "c", "identity") {
		t.Errorf("paths = %+v", got)
	}
	if text := mustRun(t, "paths"); !strings.Contains(text, "socket") || !strings.Contains(text, got.Socket) {
		t.Errorf("text:\n%s", text)
	}
	if _, err := acmd(t, "paths", "extra"); err == nil {
		t.Error("took an argument")
	}
}
