package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/brettmostert/agent-command/internal/agycli"
	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// maxAgentSessions limits how many of each agent's own sessions are read.
const maxAgentSessions = 300

// hostListTimeout is how long a host gets to answer session.list: one that
// doesn't is named as unreached, and the rest is listed.
var hostListTimeout = 5 * time.Second

// listed is one row of session.list: a session, and what an agent's own
// record adds to it.
type listed struct {
	s      *core.Session
	branch string
	live   bool
}

func (l listed) wire() *ipc.Session {
	w := ipc.SessionOf(l.s)
	w.Branch, w.AgentLive = l.branch, l.live
	return w
}

// sessionList is session.list: this machine's sessions and the known hosts',
// narrowed by the filter, the most recently active first.
func (d *Daemon) sessionList(ctx context.Context, req ipc.SessionListRequest) (ipc.SessionPage, error) {
	f := req.Filter
	hosts, err := d.hostsFor(f)
	if err != nil {
		return ipc.SessionPage{}, err
	}
	var rows []*ipc.Session
	if f.Here() {
		here, err := d.sessionsHere(ctx, f)
		if err != nil {
			return ipc.SessionPage{}, err
		}
		for _, l := range here {
			rows = append(rows, l.wire())
		}
	}
	var unreached []string
	if f.Resumable {
		var more []*ipc.Session
		more, unreached = d.resumableOnHosts(ctx, f, hosts)
		rows = append(rows, more...)
	} else {
		for _, s := range d.client.Tree().Sessions {
			if slices.Contains(hosts, s.Host) && f.Match(s.Core()) {
				rows = append(rows, s)
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].LastEventAt.After(rows[j].LastEventAt) })
	items, next, err := ipc.PageOf(rows, req.Page)
	if err != nil {
		return ipc.SessionPage{}, badRequest("%v", err)
	}
	if items == nil {
		items = []*ipc.Session{}
	}
	return ipc.SessionPage{Items: items, NextCursor: next, Unreached: unreached}, nil
}

// hostsFor is the known hosts the filter takes in. A name that isn't a known
// host is not_found, so a mistyped one says so.
func (d *Daemon) hostsFor(f ipc.SessionFilter) ([]string, error) {
	for _, on := range f.On {
		if on != ipc.OnLocal && on != ipc.OnRemote && !d.client.Knows(on) {
			return nil, &codedError{code: ipc.CodeNotFound, err: errors.New("there's no Remote host named " + quote(on))}
		}
	}
	var out []string
	for _, h := range d.client.Hosts() {
		if f.OnHost(h.Name) {
			out = append(out, h.Name)
		}
	}
	return out, nil
}

// sessionsHere is this machine's part of the list. With Resumable, that is
// the stopped sessions, and the agents' own sessions acmd doesn't have; one
// acmd runs now isn't resumable, so it isn't listed.
func (d *Daemon) sessionsHere(ctx context.Context, f ipc.SessionFilter) ([]listed, error) {
	d.mu.Lock()
	all, err := d.store.Sessions(ctx, true)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	d.nameHarnesses(ctx, all...)
	var out []listed
	if !f.Resumable {
		for _, s := range all {
			if f.Match(s) {
				out = append(out, listed{s: s})
			}
		}
		return out, nil
	}
	rows := map[string]*listed{} // by acmd id
	for _, s := range all {
		if resumable(s) {
			rows[s.ID] = &listed{s: s}
		}
	}
	var agentOnly []listed
	for _, r := range d.agentRecords(ctx) {
		d.mu.Lock()
		s, err := d.store.SessionByClaudeID(ctx, r.s.ClaudeSessionID)
		d.mu.Unlock()
		switch {
		case err == nil && s.Kind == core.KindHosted:
			if l := rows[s.ID]; l != nil { // acmd's own: the record adds to it
				l.branch, l.live = r.branch, r.live
				if r.s.LastEventAt.After(l.s.LastEventAt) {
					l.s.LastEventAt = r.s.LastEventAt
				}
			}
		case err == nil || errors.Is(err, store.ErrNotFound):
			agentOnly = append(agentOnly, r)
		default:
			return nil, err
		}
	}
	d.nameHarnesses(ctx, sessionsOf(agentOnly)...)
	for _, s := range all {
		if l := rows[s.ID]; l != nil && f.Match(l.s) {
			out = append(out, *l)
		}
	}
	for _, l := range agentOnly {
		if f.Match(l.s) {
			out = append(out, l)
		}
	}
	return out, nil
}

func sessionsOf(ls []listed) []*core.Session {
	out := make([]*core.Session, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.s)
	}
	return out
}

// resumable says whether s can be resumed: it's stopped, or archived with an
// agent session to come back to (resuming brings it back).
func resumable(s *core.Session) bool {
	if s.Kind != core.KindHosted || s.Host != "" {
		return false
	}
	if s.Status == core.StatusArchived {
		return !s.IsTerminal() && s.ClaudeSessionID != ""
	}
	return slices.Contains(s.Capabilities(), core.CanResume)
}

// resumableOnHosts asks each host for what it can resume, all at once. A
// host that doesn't answer in time is unreached.
func (d *Daemon) resumableOnHosts(ctx context.Context, f ipc.SessionFilter, hosts []string) ([]*ipc.Session, []string) {
	type answer struct {
		rows []*ipc.Session
		err  error
	}
	answers := make([]answer, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := d.listOnHost(ctx, host, f)
			answers[i] = answer{rows, err}
		}()
	}
	wg.Wait()
	var rows []*ipc.Session
	var unreached []string
	for i, a := range answers {
		if a.err != nil {
			d.cfg.Log.Printf("list %s's sessions: %v", hosts[i], a.err)
			unreached = append(unreached, hosts[i])
			continue
		}
		rows = append(rows, a.rows...)
	}
	return rows, unreached
}

// listOnHost is a host's own resumable sessions, every page, as they are
// from here: named by the host, and without what only its own machine can do.
func (d *Daemon) listOnHost(ctx context.Context, host string, f ipc.SessionFilter) ([]*ipc.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, hostListTimeout)
	defer cancel()
	f.On = []string{ipc.OnLocal}
	var out []*ipc.Session
	req := ipc.SessionListRequest{Filter: f, Page: ipc.Page{Limit: ipc.PageMax}}
	for {
		raw, err := d.client.Call(ipc.WithClient(ctx, d.uiOf(ctx)), host, ipc.OpSessionList, mustJSON(req))
		if err != nil {
			return nil, err
		}
		var page ipc.SessionPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("%s's list: %w", host, err)
		}
		for _, s := range page.Items {
			s.Host = host
			if s.ID != "" {
				s.RemoteID = s.ID
			}
			s.Capabilities = slices.DeleteFunc(s.Capabilities, func(c string) bool {
				return c == core.CanOpenInEditor || c == core.CanContinueInJob
			})
			out = append(out, s)
		}
		if page.NextCursor == "" || len(page.Items) == 0 {
			return out, nil
		}
		req.Cursor = page.NextCursor
	}
}

// agentRecords are the agents' own sessions on this machine, as rows with no
// acmd id: a Claude harness's from its own folder only (two on one folder
// list them once), and agy's, which has one history, as its first harness's.
// A client-only daemon has none.
func (d *Daemon) agentRecords(ctx context.Context) []listed {
	if d.cfg.ClientOnly {
		return nil
	}
	var out []listed
	seen := map[string]bool{}
	agyDone := false
	record := func(h core.Harness, id, cwd, title, branch string, at time.Time) {
		out = append(out, listed{s: &core.Session{ClaudeSessionID: id, HarnessKind: h.Kind, HarnessID: h.ID, Cwd: cwd,
			Name: title, NameSource: core.NameAuto, LastEventAt: at}, branch: branch})
	}
	for _, h := range d.harnessList(ctx) {
		switch h.Kind {
		case core.HarnessClaude:
			dir := d.claudeDir(h)
			if seen[dir] || dir == "" {
				continue
			}
			seen[dir] = true
			found, err := claudecli.Conversations(dir, maxAgentSessions)
			if err != nil {
				d.cfg.Log.Printf("list %s's sessions: %v", h.Name, err)
				continue
			}
			for _, c := range found {
				record(h, c.ID, c.Cwd, c.Title, c.Branch, c.UpdatedAt)
			}
		case core.HarnessAntigravity:
			if agyDone || d.cfg.AgyDir == "" {
				continue
			}
			agyDone = true
			found, err := agycli.Conversations(d.cfg.AgyDir, maxAgentSessions)
			if err != nil {
				d.cfg.Log.Printf("list Antigravity sessions: %v", err)
			}
			for _, c := range found {
				record(h, c.ID, c.Cwd, c.Title, "", c.UpdatedAt)
			}
		}
	}
	live := d.liveAgentSessions(ctx)
	for i := range out {
		out[i].live = live[out[i].s.ClaudeSessionID]
	}
	return out
}

// liveAgentSessions is what Claude reports as running. It is best effort:
// without it, the list just can't warn about one that is still live.
func (d *Daemon) liveAgentSessions(ctx context.Context) map[string]bool {
	running := map[string]bool{}
	if d.cfg.Agents == nil {
		return running
	}
	agents, err := claudeAgents{d}.Agents(ctx)
	if err != nil {
		d.cfg.Log.Printf("list running agent sessions: %v", err)
		return running
	}
	for _, a := range agents {
		if !a.Stopped() {
			running[a.SessionID] = true
		}
	}
	return running
}

// resumeAgentSession continues one of an agent's own sessions in a host, and
// tracks it from then on. One acmd already has is that session, resumed (and
// brought back from the archive), not a second one.
func (d *Daemon) resumeAgentSession(ctx context.Context, req ipc.NewRequest) (*core.Session, error) {
	c, err := d.findAgentSession(ctx, req)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	s, err := d.store.SessionByClaudeID(ctx, c.ClaudeSessionID)
	if err == nil && s.Status == core.StatusArchived {
		core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
		err = d.store.UpdateSession(ctx, s)
	}
	d.mu.Unlock()
	switch {
	case err == nil:
		return d.resume(ctx, s.ID)
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	req.Cwd, req.Resume, req.HarnessID, req.HarnessKind = c.Cwd, c.ClaudeSessionID, c.HarnessID, c.HarnessKind
	if req.Name == "" {
		req.Name = c.Name
	}
	return d.hostSession(ctx, req)
}

// findAgentSession finds the agent's own session a request resumes, in its
// harness's history (the default harness's when it names none).
func (d *Daemon) findAgentSession(ctx context.Context, req ipc.NewRequest) (core.Session, error) {
	h, err := d.newHarness(ctx, req.HarnessID, req.HarnessKind)
	if err != nil {
		return core.Session{}, err
	}
	if h == nil {
		return core.Session{}, fmt.Errorf("a terminal has no agent session to resume")
	}
	found := core.Session{HarnessID: h.ID, HarnessKind: h.Kind}
	switch h.Kind {
	case core.HarnessAntigravity:
		c, err := agycli.FindConversation(d.cfg.AgyDir, req.Resume)
		found.ClaudeSessionID, found.Cwd, found.Name = c.ID, c.Cwd, c.Title
		return found, err
	default:
		c, err := claudecli.FindConversation(d.claudeDir(*h), req.Resume)
		found.ClaudeSessionID, found.Cwd, found.Name = c.ID, c.Cwd, c.Title
		return found, err
	}
}
