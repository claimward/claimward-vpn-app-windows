// SPDX-License-Identifier: BSD-3-Clause

// Command claimward-app is the Claimward VPN desktop app for Windows: a
// window and a notification-area (tray) icon, drawn by go-widgets, that
// signs the person in, lets them choose a tenant, and asks the privileged
// helper service (cmd/claimward-helper) to bring the tunnel up and down.
//
// It runs unprivileged. It builds and runs on macOS and Linux too, which is
// how the UI is developed; the tunnel needs the Windows helper.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/claimward/claimward-vpn-app-windows/internal/brand"
	"github.com/claimward/claimward-vpn-app-windows/internal/ui"
	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/go-widgets/application"
	"github.com/go-widgets/tray"
)

// windowTitle is also how the tray finds the window to bring it forward.
const windowTitle = "Claimward VPN"

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "claimward-app:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := appcore.LoadConfig()
	if err != nil {
		return fmt.Errorf("load %s: %w", appcore.ConfigPath(), err)
	}
	core := appcore.New(cfg)

	queue := &ui.Queue{}
	vm := ui.NewViewModel(core,
		ui.WithDispatch(func(f func()) { go f() }, queue.Post),
		ui.WithOpener(openURL),
		ui.WithConfigPath("Saved to "+appcore.ConfigPath()),
	)
	if err := ui.UseOpenTypeText(); err != nil {
		fmt.Fprintln(os.Stderr, "claimward-app: falling back to the bitmap font:", err)
	}
	view := ui.NewView(vm, queue)
	defer view.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm.StartPolling(ctx, 2*time.Second)

	t := tray.New(brand.TrayIcon).SetTooltip("Claimward")
	unbind := ui.BindTray(t, vm, queue.Post, ui.TrayActions{
		Open: func() { raiseWindow(windowTitle) },
		Quit: func() { closeWindow(windowTitle) },
	})
	defer unbind()
	defer t.Quit()

	spec := application.Spec{Name: "Claimward", Identifier: "org.claimward.vpn", Version: version}
	return application.Run(spec, application.Config{Title: windowTitle, Width: 460, Height: 720}, view, func() {
		// The tray joins once the window's loop runs, as application.Run
		// does for a Spec.Tray; it is attached here so that the binding
		// can change its menu.
		go func() { _ = t.Attach() }()
	})
}
