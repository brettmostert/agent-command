package client

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/term"
)

// disconnected says whether err is a disconnected host's refusal.
func disconnected(err error) bool {
	var ce *wire.CallError
	return errors.As(err, &ce) && ce.Code == ipc.CodeHostUnreachable && strings.Contains(ce.Message, "is disconnected: connect it first")
}

func TestDisconnectAHost(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	l.know(r)
	l.waitState("studio", ipc.HostConnected)
	eventually(t, "its session", func() bool { return l.c.Is("r1") })

	st, err := l.c.Disconnect("studio")
	must(t, err)
	if st.State != ipc.HostDisconnected {
		t.Errorf("disconnect = %+v", st)
	}
	// The host sees the connection go, and nothing dials it again.
	eventually(t, "the host to see it go", func() bool { return len(r.h.Clients()[0].Connections) == 0 })
	n := l.dials.count()
	time.Sleep(300 * time.Millisecond)
	if got := l.dials.count(); got != n {
		t.Errorf("dialed %d times while disconnected", got-n)
	}
	if st := l.state("studio"); st.State != ipc.HostDisconnected || st.Error != "" {
		t.Errorf("state = %+v", st)
	}
	// Its sessions stay here, offline.
	if !l.c.Is("r1") {
		t.Error("its session went")
	}
	// Whatever needs it is refused at once, and doesn't connect it.
	start := time.Now()
	if _, err := l.c.Browse(ctx5(), "studio"); !disconnected(err) {
		t.Errorf("browse: %v", err)
	}
	if _, err := l.c.Call(ctx5(), "studio", ipc.OpSnapshot, nil); !disconnected(err) {
		t.Errorf("call: %v", err)
	}
	if time.Since(start) > time.Second || l.dials.count() != n {
		t.Errorf("took %v, and dialed %d times", time.Since(start), l.dials.count()-n)
	}
	// Disconnecting it again changes nothing.
	_, err = l.c.Disconnect("studio")
	must(t, err)
	if _, err := l.c.Disconnect("nowhere"); err == nil {
		t.Error("disconnected a host that isn't known")
	}

	// It stays disconnected across a restart.
	if l.data.Disconnected[0] != "studio" {
		t.Errorf("kept = %v", l.data.Disconnected)
	}
	l.c.Close()
	l.start()
	time.Sleep(200 * time.Millisecond)
	if st := l.state("studio"); st.State != ipc.HostDisconnected || l.dials.count() != n {
		t.Errorf("after a restart: %+v, %d dials", st, l.dials.count()-n)
	}

	// A rename keeps it disconnected, and an edit doesn't connect it.
	_, err = l.c.EditHost(ctx5(), ipc.RemoteHostEdit{Host: "studio", Name: "studio-mac"})
	must(t, err)
	time.Sleep(200 * time.Millisecond)
	if st := l.state("studio-mac"); st.State != ipc.HostDisconnected || l.dials.count() != n {
		t.Errorf("after a rename: %+v, %d dials", st, l.dials.count()-n)
	}
	if len(l.data.Disconnected) != 1 || l.data.Disconnected[0] != "studio-mac" {
		t.Errorf("kept after a rename = %v", l.data.Disconnected)
	}

	// Connect connects it at once, and it's no longer kept.
	st, err = l.c.Connect("studio-mac")
	must(t, err)
	if st.State != ipc.HostConnecting {
		t.Errorf("connect = %+v", st)
	}
	l.waitState("studio-mac", ipc.HostConnected)
	if len(l.data.Disconnected) != 0 {
		t.Errorf("still kept: %v", l.data.Disconnected)
	}
	if _, err := l.c.Browse(ctx5(), "studio-mac"); err != nil {
		t.Errorf("browse once connected: %v", err)
	}

	// Removing a disconnected host forgets that it was.
	_, err = l.c.Disconnect("studio-mac")
	must(t, err)
	must(t, l.c.RemoveHost(ctx5(), "studio-mac"))
	if len(l.data.Disconnected) != 0 {
		t.Errorf("kept after removing it: %v", l.data.Disconnected)
	}
}

func TestDisconnectAConfigHost(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	l.waitState("studio", ipc.HostConnected)
	// A host the data says was disconnected, as config.toml's would be.
	must(t, l.data.SetHostDisconnected("studio", true))
	must(t, l.c.Reload())
	l.waitState("studio", ipc.HostDisconnected)
	must(t, l.data.SetHostDisconnected("studio", false))
	must(t, l.c.Reload())
	l.waitState("studio", ipc.HostConnected)
}

func TestDisconnectWhileDialling(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	l.waitState("studio", ipc.HostConnected)
	r.h.Close() // nobody answers: it keeps trying
	l.waitState("studio", ipc.HostUnreachable)
	_, err := l.c.Disconnect("studio")
	must(t, err)
	n := l.dials.count()
	time.Sleep(300 * time.Millisecond)
	if st := l.state("studio"); st.State != ipc.HostDisconnected || l.dials.count() > n+1 {
		t.Errorf("state = %+v, %d dials", st, l.dials.count()-n)
	}
}

func TestDisconnectedFailsSaving(t *testing.T) {
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	l.know(r)
	l.data.Fail = errors.New("disk full")
	if _, err := l.c.Disconnect("studio"); err == nil {
		t.Error("disconnected without keeping it")
	}
	l.waitState("studio", ipc.HostConnected)
}

func TestAPaneWaitsForADisconnectedHost(t *testing.T) {
	realShell(t)
	l := newLaptop(t)
	r := newRemoteHost(t, "studio")
	r.authorize(l.fingerprint())
	r.b.add(remoteSession("r1", "api"))
	sock := r.terminal("r1")
	l.know(r)
	eventually(t, "its socket", func() bool { return l.c.Is("r1") })
	_, err := l.c.Disconnect("studio")
	must(t, err)
	// A pane opened meanwhile waits, and shows the session once it's connected.
	p := attachPane(t, l.o.Socket("r1"))
	must(t, term.Send(sock, []byte("echo back-$((2+3))\n")))
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(p.out.String(), "back-5") {
		t.Error("relayed while disconnected")
	}
	_, err = l.c.Connect("studio")
	must(t, err)
	eventually(t, "the pane", func() bool { return strings.Contains(p.out.String(), "back-5") })
}
