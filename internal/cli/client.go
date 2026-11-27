package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/hook"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/version"
)

// client talks to the daemon, starting it (or replacing one of another
// version) when needed.
type client struct {
	conn *ipc.Conn
	// host is the Remote host the command runs against (--host), "" for this
	// machine.
	host string
	// terminal is this machine's terminal client id, when it connected as
	// one (connectTerminal).
	terminal string
}

// targetHost is --host, or ACMD_HOST: the Remote host commands run against.
var targetHost string

// call runs a daemon op: this machine's, or with --host the host's, through
// this machine's daemon (remote.call), which the host answers as its own
// socket would.
//
// With --json, its answer is kept to print (record).
func (c *client) call(op string, in, out any) error {
	var raw json.RawMessage
	if c.host == "" {
		if err := c.conn.Call(op, in, &raw); err != nil {
			return err
		}
	} else {
		rc := ipc.RemoteCall{Host: c.host, Op: op}
		if in != nil {
			b, err := json.Marshal(in)
			if err != nil {
				return err
			}
			rc.Data = b
		}
		if err := c.hostCall(c.host, ipc.OpRemoteCall, rc, &raw); err != nil {
			return err
		}
	}
	record(op, raw)
	if out == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// spawnDaemon is swappable in tests.
var spawnDaemon = hook.SpawnDaemon

func connect(ctx context.Context) (*client, error) {
	return connectAs(ctx, ipc.HelloFrom(version.Get().Version))
}

// connectAs is connect with the hello me: a terminal client names itself in
// it (terminalHello).
func connectAs(ctx context.Context, me ipc.Hello) (*client, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	conn, err := ipc.Dial(ctx, p.Socket(), me)
	if err == nil {
		return &client{conn: conn, host: targetHost}, nil
	}
	var mismatch *ipc.ErrVersionMismatch
	if errors.As(err, &mismatch) {
		// After an upgrade: the service restarts its daemon, so none runs
		// outside it.
		if svc, ok := installedService(); ok {
			_ = conn.Close()
			conn, err := restartService(ctx, p, svc)
			if err != nil {
				return nil, err
			}
			return &client{conn: conn, host: targetHost}, nil
		}
		_ = conn.Call(ipc.OpShutdown, nil, nil)
		_ = conn.Close()
		waitDaemonGone(ctx, p)
	}
	if err := spawnDaemon(p); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err = ipc.Dial(ctx, p.Socket(), me)
		if err == nil {
			return &client{conn: conn, host: targetHost}, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("daemon didn't start (see %s): %w", p.DaemonLog(), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *client) close() { _ = c.conn.Close() }

func (c *client) snapshot() (snapshot, error) {
	var s ipc.Snapshot
	err := c.call(ipc.OpSnapshot, nil, &s)
	return snapshotOf(s), err
}

// snapshot is the daemon's snapshot with its sessions and groups as
// internal/core has them, for the CLI's layout and lookups. The view is
// the command's own: the daemon keeps none.
type snapshot struct {
	ipc.Snapshot
	Sessions []*core.Session `json:"-"`
	Groups   []*core.Group   `json:"-"`
}

func snapshotOf(s ipc.Snapshot) snapshot {
	return snapshot{Snapshot: s, Sessions: s.CoreSessions(), Groups: s.CoreGroups()}
}

// A kind that changes many sessions answers a result for each; with no out
// to take them, the first that failed is the error, with its code.
func (c *client) mutate(m ipc.Mutation, out any) error {
	if out != nil {
		return c.call(ipc.OpMutate, m, out)
	}
	var raw json.RawMessage
	if err := c.call(ipc.OpMutate, m, &raw); err != nil {
		return err
	}
	var res ipc.Results
	if json.Unmarshal(raw, &res) == nil {
		for _, r := range res.Results {
			if !r.OK {
				return &ipc.RemoteError{Msg: r.Error, Code: r.Code}
			}
		}
	}
	return nil
}

// withClient runs fn with a connected client and a fresh snapshot.
func withClient(cmdCtx context.Context, fn func(*client, snapshot) error) error {
	ctx, cancel := context.WithTimeout(cmdCtx, 30*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	snap, err := c.snapshot()
	if err != nil {
		return err
	}
	return fn(c, snap)
}

// resolveSession finds a session by its acmd id or its Claude session id.
// Nothing else picks one: acmd find turns a name into ids.
func resolveSession(snap snapshot, id string) (*core.Session, error) {
	return core.Resolve(snap.Sessions, id)
}

// resolveGroup finds one of this machine's groups by its id.
func resolveGroup(snap snapshot, id string) (*core.Group, error) {
	return resolveGroupOn(snap, id, "")
}

// resolveGroupOn is resolveGroup among host's groups ("" for this
// machine's): ids are unique only within their host. Nothing but an id
// picks a group: acmd group find turns a name into ids.
func resolveGroupOn(snap snapshot, id, host string) (*core.Group, error) {
	id = strings.TrimSpace(id)
	for _, g := range snap.Groups {
		if g.Host == host && g.ID == id {
			return g, nil
		}
	}
	if host != "" {
		return nil, fmt.Errorf("%s has no group %q: give its id (acmd group find <name> finds it)", host, id)
	}
	return nil, fmt.Errorf("no group %q: give its id (acmd group find <name> finds it)", id)
}

// liveGroupOn is resolveGroupOn for a group sessions go into: an archived
// one is refused before anything is sent.
func liveGroupOn(snap snapshot, id, host string) (*core.Group, error) {
	g, err := resolveGroupOn(snap, id, host)
	if err != nil {
		return nil, err
	}
	if !g.ArchivedAt.IsZero() {
		return nil, fmt.Errorf("%s is archived: unarchive it first (acmd group unarchive %s)", g.Name, g.ID)
	}
	return g, nil
}

// confirm asks a yes/no question on a terminal; without a terminal it
// requires --yes.
func confirm(in io.Reader, out io.Writer, yes bool, question string) error {
	if yes {
		return nil
	}
	if f, ok := in.(*os.File); !ok || !isTerminal(f) {
		return errors.New("refusing without confirmation: pass --yes")
	}
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		return nil
	}
	return errors.New("canceled")
}

// isTerminal says whether f is a terminal. A character device isn't enough:
// /dev/null is one, and it's the input of anything the app or launchd runs.
func isTerminal(f *os.File) bool {
	return xterm.IsTerminal(f.Fd())
}

// hasCode is whether err is the daemon's error with code.
func hasCode(err error, code string) bool {
	var re *ipc.RemoteError
	return errors.As(err, &re) && re.Code == code
}

// shellQuote quotes s for a command to copy, when it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
