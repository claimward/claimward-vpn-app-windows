// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/go-widgets/tray"
)

// fakeTray records what the binding sets.
type fakeTray struct {
	menus    []*tray.Menu
	tooltips []string
}

func (f *fakeTray) SetMenu(m *tray.Menu) *tray.Tray  { f.menus = append(f.menus, m); return nil }
func (f *fakeTray) SetTooltip(s string) *tray.Tray   { f.tooltips = append(f.tooltips, s); return nil }
func (f *fakeTray) last() *tray.Menu                 { return f.menus[len(f.menus)-1] }
func item(m *tray.Menu, label string) *tray.MenuItem { return m.ByLabel(label) }

func TestTheTrayFollowsTheViewModel(t *testing.T) {
	f := signedIn()
	vm, q := newVM(t, f)
	tr := &fakeTray{}
	opened, quit := 0, 0
	unbind := BindTray(tr, vm, q.Post, TrayActions{Open: func() { opened++ }, Quit: func() { quit++ }})
	defer unbind()

	m := tr.last()
	if got := m.All()[0]; got.Label != "Claimward: disconnected" || !got.Disabled {
		t.Errorf("status row %q disabled=%v", got.Label, got.Disabled)
	}
	if item(m, "Connect").Disabled || !item(m, "Disconnect").Disabled {
		t.Error("signed in: Connect should be live and Disconnect greyed")
	}
	if tr.tooltips[len(tr.tooltips)-1] != "Claimward: disconnected" {
		t.Errorf("tooltip %q", tr.tooltips[len(tr.tooltips)-1])
	}

	// A click arrives on the tray's thread: it is posted, and runs on the
	// UI thread when the queue drains.
	item(m, "Connect").Activate()
	if f.called("Connect") != 0 {
		t.Fatal("the tray ran a command off the UI thread")
	}
	q.Drain()
	settle(vm, q)
	if f.called("Connect") != 1 {
		t.Fatalf("Connect called %d times", f.called("Connect"))
	}
	m = tr.last()
	if !strings.HasPrefix(m.All()[0].Label, "Claimward: connected") || !item(m, "Connect").Disabled || item(m, "Disconnect").Disabled {
		t.Errorf("connected: %q", m.All()[0].Label)
	}
	item(m, "Disconnect").Activate()
	item(m, "Open Claimward").Activate()
	item(m, "Quit").Activate()
	q.Drain()
	settle(vm, q)
	if f.called("Disconnect") != 1 || opened != 1 || quit != 1 {
		t.Errorf("disconnect %d open %d quit %d", f.called("Disconnect"), opened, quit)
	}

	// A poll that changes nothing does not re-send the menu.
	n := len(tr.menus)
	vm.Refresh()
	q.Drain()
	vm.Refresh()
	q.Drain()
	if len(tr.menus) != n {
		t.Errorf("an unchanged status re-sent the menu %d times", len(tr.menus)-n)
	}
}

func TestTheTrayOffersNothingWhileBusy(t *testing.T) {
	f := signedIn()
	f.connectWait = make(chan struct{})
	vm, q := newVM(t, f)
	tr := &fakeTray{}
	BindTray(tr, vm, q.Post, TrayActions{})
	vm.Connect.Execute()
	deadline := time.Now().Add(5 * time.Second)
	for f.called("Connect") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	q.Drain()
	if mm := tr.last(); !item(mm, "Connect").Disabled || !item(mm, "Disconnect").Disabled {
		t.Error("the tray offers Connect while connecting")
	}
	close(f.connectWait)
	settle(vm, q)
}

type notifier struct {
	got []tray.Notification
	err error
}

func (n *notifier) Notify(x tray.Notification) error { n.got = append(n.got, x); return n.err }

func TestADroppedTunnelIsOneNotificationNotOnePerPoll(t *testing.T) {
	vm := NewViewModel(signedIn())
	n := &notifier{}
	opened := 0
	var logged []string
	unbind := BindNotifications(n, vm, func() { opened++ }, func(f string, a ...any) { logged = append(logged, f) })
	defer unbind()

	up := appcore.Status{ConfigOK: true, LoggedIn: true, HelperInstalled: true, Connected: true}
	down := up
	down.Connected = false
	vm.Apply(up)
	for i := 0; i < 10; i++ {
		vm.Apply(down)
	}
	if len(n.got) != 1 || n.got[0].Title == "" || n.got[0].Body == "" {
		t.Fatalf("10 polls in the dropped state gave %d notification(s): %+v", len(n.got), n.got)
	}
	n.got[0].OnClick()
	if opened != 1 {
		t.Fatalf("a click opened the window %d times", opened)
	}
	if len(logged) != 0 {
		t.Fatalf("logged %v", logged)
	}

	// A notification that cannot be shown is logged and stops nothing.
	n.err = context.DeadlineExceeded
	vm.Apply(up)
	vm.Apply(down)
	if len(n.got) != 2 || len(logged) != 1 {
		t.Fatalf("got %d, logged %v", len(n.got), logged)
	}

	// Once detached, a drop notifies no one.
	unbind()
	vm.Apply(up)
	vm.Apply(down)
	if len(n.got) != 2 {
		t.Fatal("a detached binding notified")
	}
}

func TestThePersonsOwnDisconnectIsNotNotified(t *testing.T) {
	f := signedIn()
	f.st.Connected = true
	vm, q := newVM(t, f)
	n := &notifier{}
	defer BindNotifications(n, vm, func() {}, t.Logf)()
	run(vm, q, vm.Disconnect.Execute)
	if len(n.got) != 0 {
		t.Fatalf("the person's own Disconnect was announced: %+v", n.got)
	}
}
