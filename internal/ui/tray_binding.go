// SPDX-License-Identifier: BSD-3-Clause
//
// The tray's binding to the ViewModel: the menu is rebuilt from the
// ViewModel's observables and commands when they change, never polled. Tray
// clicks arrive on the tray's own thread and are posted to the UI thread,
// where the commands run like a button's.

package ui

import (
	"strings"

	"github.com/go-widgets/tray"
)

// TrayActions are what the tray does outside the ViewModel: bring the
// window forward, and quit.
type TrayActions struct {
	Open, Quit func()
}

// TrayMenu is the tray menu for the ViewModel's current state: the status
// (not clickable), Connect and Disconnect (greyed out when they cannot
// run), Open and Quit. Every click is handed to post.
func TrayMenu(vm *ViewModel, post func(func()), act TrayActions) *tray.Menu {
	status := tray.Item(vm.TrayStatus.Get(), nil)
	status.Disabled = true
	connect := tray.Item("Connect", func() { post(vm.Connect.Execute) })
	connect.Disabled = !vm.Connect.CanExecute()
	disconnect := tray.Item("Disconnect", func() { post(vm.Disconnect.Execute) })
	disconnect.Disabled = !vm.Disconnect.CanExecute()
	return tray.NewMenu().Add(
		status,
		tray.Separator(),
		connect,
		disconnect,
		tray.Separator(),
		tray.Item("Open Claimward", func() { post(act.Open) }),
		tray.Item("Quit", func() { post(act.Quit) }),
	)
}

// trayShape is what the menu shows, to rebuild it only when that changes:
// the status is re-applied every poll, and a tray re-sent its menu every
// two seconds for nothing.
func trayShape(m *tray.Menu) string {
	var b strings.Builder
	for _, it := range m.All() {
		b.WriteString(it.Label)
		if it.Disabled {
			b.WriteString("\x00off")
		}
		b.WriteString("\x00")
	}
	return b.String()
}

// Tray is what BindTray drives: *tray.Tray.
type Tray interface {
	SetMenu(*tray.Menu) *tray.Tray
	SetTooltip(string) *tray.Tray
}

// BindTray keeps t's menu and tooltip in step with the ViewModel. It must be
// called, and the ViewModel changed, on the UI thread.
func BindTray(t Tray, vm *ViewModel, post func(func()), act TrayActions) (unbind func()) {
	last := ""
	sync := func() {
		m := TrayMenu(vm, post, act)
		if s := trayShape(m); s != last {
			last = s
			t.SetMenu(m)
			t.SetTooltip(vm.TrayStatus.Get())
		}
	}
	sync()
	unsubs := []func(){
		vm.TrayStatus.SubscribeChanged(sync),
		vm.Connect.SubscribeCanExecuteChanged(sync),
		vm.Disconnect.SubscribeCanExecuteChanged(sync),
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}
