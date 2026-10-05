// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"strings"
	"testing"

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
	q := &Queue{}
	vm := NewViewModel(f, WithDispatch(func(fn func()) { fn() }, q.Post))
	vm.Refresh()
	q.Drain()
	tr := &fakeTray{}
	opened, quit := 0, 0
	unbind := BindTray(tr, vm, q.Post, TrayActions{Open: func() { opened++ }, Quit: func() { quit++ }})
	defer unbind()

	m := tr.last()
	if got := m.All()[0]; got.Label != "Claimward: Disconnected" || !got.Disabled {
		t.Errorf("status row %q disabled=%v", got.Label, got.Disabled)
	}
	if item(m, "Connect").Disabled || !item(m, "Disconnect").Disabled {
		t.Error("signed in: Connect should be live and Disconnect greyed")
	}
	if tr.tooltips[len(tr.tooltips)-1] != "Claimward: Disconnected" {
		t.Errorf("tooltip %q", tr.tooltips[len(tr.tooltips)-1])
	}

	// A click arrives on the tray's thread: it is posted, and runs on the
	// UI thread when the queue drains.
	item(m, "Connect").Activate()
	if f.called("Connect") != 0 {
		t.Fatal("the tray ran a command off the UI thread")
	}
	q.Drain()
	if f.called("Connect") != 1 {
		t.Fatalf("Connect called %d times", f.called("Connect"))
	}
	m = tr.last()
	if !strings.HasPrefix(m.All()[0].Label, "Claimward: Connected") || !item(m, "Connect").Disabled || item(m, "Disconnect").Disabled {
		t.Errorf("connected: %q", m.All()[0].Label)
	}
	item(m, "Disconnect").Activate()
	item(m, "Open Claimward").Activate()
	item(m, "Quit").Activate()
	q.Drain()
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
	// While busy, neither Connect nor Disconnect is offered.
	m2 := &manual{}
	vm2 := NewViewModel(signedIn(), WithDispatch(m2.spawn, m2.q.Post))
	vm2.Refresh()
	m2.runWork()
	tr2 := &fakeTray{}
	BindTray(tr2, vm2, m2.q.Post, TrayActions{})
	vm2.Connect.Execute()
	if mm := tr2.last(); !item(mm, "Connect").Disabled || !item(mm, "Disconnect").Disabled {
		t.Error("the tray offers Connect while connecting")
	}
}
