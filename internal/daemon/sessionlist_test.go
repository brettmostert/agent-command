package daemon

import (
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/host"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
	"github.com/brettmostert/agent-command/internal/remote/identity"
)

// A host whose Hosting this machine is paired with, in memory.
func pairedHost(t *testing.T, p interface{ RemoteIdentityDir() string }, dir string) (*otherDaemon, *host.Host) {
	t.Helper()
	b := &otherDaemon{sockets: map[string]string{}, ch: make(chan struct{})}
	cfg := filepath.Join(dir, "config.toml")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	must(t, writeSetting(cfg, "remote.host", "enabled", true))
	must(t, writeSetting(cfg, "remote.host", "port", port))
	file := config.OpenFile(cfg)
	b.file = file
	h := host.New(host.Options{Settings: func() config.Remote { return file.Config().Remote }, IdentityDir: filepath.Join(dir, "id")})
	t.Cleanup(h.Close)
	hostapi.Register(h, b, hostapi.Options{Home: dir})
	h.Reload()
	me, err := identity.Ensure(p.RemoteIdentityDir())
	must(t, err)
	_, err = h.Authorize(me.Fingerprint(), "laptop")
	must(t, err)
	_, err = h.SetScope("laptop", config.ScopeAdmin)
	must(t, err)
	return b, h
}

// session.list lists this machine's sessions and each known host's, newest
// first. With resumable, each host is asked for its own, all at once; one
// that doesn't answer is named, and the rest is still listed. on narrows it
// to here, any host or one host, and a host's agent session resumes there.
func TestSessionListAcrossHosts(t *testing.T) {
	sessionsHere(t)
	old := hostListTimeout
	hostListTimeout = 2 * time.Second
	t.Cleanup(func() { hostListTimeout = old })

	p := testPaths(t)
	base := filepath.Dir(p.StateDir)
	p.ConfigDir = filepath.Join(base, "c")
	b, h := pairedHost(t, p, filepath.Join(base, "o"))
	now := time.Now().UTC()
	b.change(func() {
		b.sessions = []*core.Session{{ID: "r1", Name: "api", Cwd: "/s/api", HarnessKind: core.HarnessClaude, HarnessName: "Work",
			Status: core.StatusBusy, Remote: true, CreatedAt: now, LastEventAt: now}}
		b.resumable = []*ipc.Session{
			{ID: "", ClaudeSessionID: "aaaaaaaa-1111-4111-8111-111111111111", Name: "studio work", Cwd: "/s/work",
				HarnessKind: core.HarnessClaude, HarnessID: "hw", HarnessName: "Work", LastEventAt: now.Add(-time.Minute)},
			{ID: "r7", ClaudeSessionID: "bbbbbbbb-2222-4222-8222-222222222222", Name: "old", Status: core.StatusFinished,
				HarnessKind: core.HarnessClaude, HarnessName: "Claude", LastEventAt: now.Add(-3 * time.Hour),
				Capabilities: []string{core.CanResume, core.CanOpenInEditor, core.CanContinueInJob}},
		}
	})
	claude := t.TempDir()
	const localID = "cccccccc-3333-4333-8333-333333333333"
	writeConversation(t, claude, localID, "/w/local", "local work")
	start(t, p, func(c *Config) {
		c.ConfigFile, c.ClaudeDir = p.Config(), claude
		c.Hosts = &fakeHosts{paths: p}
	})
	c := dial(t, p)
	must(t, c.Call(ipc.OpRemoteHostAdd, ipc.RemoteHostEdit{Name: "studio", Address: h.Status().Address, Fingerprint: h.Status().Fingerprint}, nil))
	// mini is known, but nothing answers at its address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	gone := ln.Addr().String()
	_ = ln.Close()
	must(t, c.Call(ipc.OpRemoteHostAdd, ipc.RemoteHostEdit{Name: "mini", Address: gone, Fingerprint: h.Status().Fingerprint}, nil))

	list := func(f ipc.SessionFilter) ipc.SessionPage {
		t.Helper()
		var page ipc.SessionPage
		must(t, c.Call(ipc.OpSessionList, ipc.SessionListRequest{Filter: f}, &page))
		return page
	}
	names := func(page ipc.SessionPage) []string {
		var out []string
		for _, s := range page.Items {
			out = append(out, s.Host+":"+s.Name)
		}
		return out
	}

	// Everywhere: the local agent session (just written, so newest), then
	// studio's two; mini is named as unreached.
	all := list(ipc.SessionFilter{Resumable: true})
	if got := names(all); !slices.Equal(got, []string{":local work", "studio:studio work", "studio:old"}) {
		t.Fatalf("resumable everywhere = %v", got)
	}
	if !slices.Equal(all.Unreached, []string{"mini"}) {
		t.Errorf("unreached = %v, want mini", all.Unreached)
	}
	work, old7 := all.Items[1], all.Items[2]
	if work.ID != "" || work.RemoteID != "" || work.HarnessName != "Work" || work.ClaudeSessionID == "" {
		t.Errorf("studio's agent session = %+v", work)
	}
	if old7.ID != "r7" || old7.RemoteID != "r7" || !slices.Equal(old7.Capabilities, []string{core.CanResume}) {
		t.Errorf("studio's stopped session = %+v: its folder and jobs aren't here", old7)
	}
	if local := all.Items[0]; local.ID != "" || local.Host != "" || local.HarnessName != "Claude" || local.Cwd != "/w/local" {
		t.Errorf("the local agent session = %+v", local)
	}
	// The host was asked for its own only.
	asked := false
	for _, call := range b.calls() {
		if strings.HasPrefix(call, ipc.OpSessionList+" ") && strings.Contains(call, `"on":["local"]`) && strings.Contains(call, `"resumable":true`) {
			asked = true
		}
	}
	if !asked {
		t.Errorf("studio wasn't asked for its own resumable sessions: %v", b.calls())
	}

	// Here only: no host is asked.
	before := len(b.calls())
	if got := names(list(ipc.SessionFilter{Resumable: true, On: []string{ipc.OnLocal}})); !slices.Equal(got, []string{":local work"}) {
		t.Errorf("here = %v", got)
	}
	if len(b.calls()) != before {
		t.Errorf("a host was asked for a local list: %v", b.calls()[before:])
	}
	// One host, by name: the other isn't asked, so isn't unreached.
	one := list(ipc.SessionFilter{Resumable: true, On: []string{"studio"}})
	if got := names(one); !slices.Equal(got, []string{"studio:studio work", "studio:old"}) || len(one.Unreached) != 0 {
		t.Errorf("studio = %v, unreached %v", got, one.Unreached)
	}
	// Any host, and a harness by name, ignoring case.
	if got := names(list(ipc.SessionFilter{Resumable: true, On: []string{ipc.OnRemote}, Harness: []string{"work"}})); !slices.Equal(got, []string{"studio:studio work"}) {
		t.Errorf("remote Work = %v", got)
	}
	// A host that isn't known says so.
	if err := c.Call(ipc.OpSessionList, ipc.SessionListRequest{Filter: ipc.SessionFilter{On: []string{"nope"}}}, nil); codeOf(err) != ipc.CodeNotFound {
		t.Errorf("an unknown host: %v, want not_found", err)
	}

	// Without resumable, a host's sessions are its tree, as it has it here.
	waitFor(t, "studio's tree", func() bool {
		got := names(list(ipc.SessionFilter{On: []string{"studio"}}))
		return slices.Equal(got, []string{"studio:api"})
	})
	if got := list(ipc.SessionFilter{On: []string{"studio"}}).Items[0]; got.HarnessName != "Work" {
		t.Errorf("a host's session has its harness's name there: %+v", got)
	}

	// Resuming a host's agent session runs it there, by its agent id.
	var s ipc.Session
	must(t, c.Call(ipc.OpRemoteNew, ipc.RemoteNewRequest{Host: "studio", HarnessID: "hw", Resume: work.ClaudeSessionID}, &s))
	resumed := false
	for _, call := range b.calls() {
		if strings.HasPrefix(call, ipc.OpNew+" ") && strings.Contains(call, `"resume":"`+work.ClaudeSessionID+`"`) && strings.Contains(call, `"harness_id":"hw"`) {
			resumed = true
		}
	}
	if !resumed || s.Host != "studio" {
		t.Errorf("resumed on studio: %+v, calls %v", s, b.calls())
	}
}

// Every session goes out with its harness's name: a renamed harness's new
// one, and its kind's title for a terminal or a harness that's gone.
func TestSessionsHaveTheirHarnessName(t *testing.T) {
	sessionsHere(t)
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p} })
	c := dial(t, p)
	var work ipc.Harness
	must(t, c.Call(ipc.OpHarnessAdd, ipc.HarnessSave{Kind: core.HarnessClaude, Name: "Work"}, &work))
	s := newSess(t, c, ipc.NewRequest{Cwd: t.TempDir(), HarnessID: work.ID})
	if s.HarnessName != "Work" {
		t.Errorf("new answers %q, want Work", s.HarnessName)
	}
	term := newSess(t, c, ipc.NewRequest{Cwd: t.TempDir(), HarnessKind: core.HarnessTerminal})
	if term.HarnessName != "Terminal" {
		t.Errorf("a terminal's harness name = %q", term.HarnessName)
	}
	must(t, c.Call(ipc.OpHarnessUpdate, ipc.HarnessSave{ID: work.ID, Kind: core.HarnessClaude, Name: "Day Job"}, nil))
	byID := func() map[string]*ipc.Session {
		var snap ipc.Snapshot
		must(t, c.Call(ipc.OpSnapshot, nil, &snap))
		out := map[string]*ipc.Session{}
		for _, x := range snap.Sessions {
			out[x.ID] = x
		}
		return out
	}
	if got := byID()[s.ID].HarnessName; got != "Day Job" {
		t.Errorf("after the rename: %q, want Day Job", got)
	}
	// session.list says the same.
	var page ipc.SessionPage
	must(t, c.Call(ipc.OpSessionList, ipc.SessionListRequest{Filter: ipc.SessionFilter{On: []string{ipc.OnLocal}, Harness: []string{"day job"}}}, &page))
	if len(page.Items) != 1 || page.Items[0].ID != s.ID {
		t.Errorf("by harness name: %+v", page.Items)
	}
}
