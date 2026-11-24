// SPDX-License-Identifier: BSD-3-Clause
//
// The ViewModel. It holds ALL of the window's and the tray's state as
// go-widgets/mvvm primitives and NEVER references a widget: the View
// (view.go) and the tray (tray_binding.go) bind to these, so state only
// ever changes here.
//
// The logic itself -- what the status means, which button is on offer, the
// sign-in, the tenant choice, the settings form, the polling -- is
// claimward-vpn-client's pkg/appvm, shared with the other apps. This file is
// the adapter: every appvm.State is copied into the observables on the UI
// thread (through the post function), and every command is a call on the
// appvm.Model. No decision is taken here.

package ui

import (
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/appvm"
	"github.com/go-widgets/mvvm"
)

// Core is what the ViewModel needs of appcore.Core.
type Core = appvm.Service

var _ Core = (*appcore.Core)(nil)

// Pages of the window, and the cards of its optional panels: each is the
// index of a card in a CardLayout the View binds to the observable.
const (
	PageStatus   = 0
	PageSettings = 1

	PanelHidden = 0
	PanelShown  = 1

	ProviderGitHub = 0
	ProviderOIDC   = 1 // also go-authn: both take an issuer and a client id
)

// Providers are the identity providers offered, in DropDown order.
var Providers = appvm.Providers

// ViewModel is the application state.
type ViewModel struct {
	m *appvm.Model
	// post brings a result back to the UI thread, the only one that touches an
	// observable.
	post func(func())

	// syncing is set while a State is copied, so that what the model says is
	// not taken for the person choosing.
	syncing bool

	// Page is the window's page (PageStatus, PageSettings).
	Page *mvvm.Observable[int]

	// State is the headline: Connected, Disconnected, Signed out, Not
	// configured. Detail is the line under it: the address and interface
	// when connected, the server otherwise, or what the configuration lacks.
	State, Detail *mvvm.Observable[string]
	// Account, Tenant and Helper are the status lines: who is signed in,
	// into which tenant, and whether the privileged helper answers.
	Account, Tenant, Helper *mvvm.Observable[string]
	// Busy says what is under way ("" when nothing is); Message is the last
	// error or notice.
	Busy, Message *mvvm.Observable[string]

	// SignInPanel shows the device-flow prompt while a sign-in waits for
	// the person: DeviceCode to type at DeviceURI.
	SignInPanel           *mvvm.Observable[int]
	DeviceURI, DeviceCode *mvvm.Observable[string]

	// TenantPanel shows the tenant choice when the server offered several,
	// or refused a connection until one is chosen. TenantNames are the
	// DropDown's rows (the "no choice" row first) and TenantIndex the selected
	// one.
	TenantPanel *mvvm.Observable[int]
	TenantNames *mvvm.ObservableList[string]
	TenantIndex *mvvm.Observable[int]

	// Log is the connection log, oldest first.
	Log *mvvm.ObservableList[string]

	// The settings form. ProviderPanel shows the GitHub or the OIDC fields.
	ServerURL, GitHubClientID, OIDCIssuer, OIDCClientID *mvvm.Observable[string]
	ProviderIndex, ProviderPanel                        *mvvm.Observable[int]
	ConfigPath                                          *mvvm.Observable[string]

	// TrayStatus is the tray's status line and tooltip.
	TrayStatus *mvvm.Observable[string]

	SignIn, CancelSignIn, OpenSignInPage      *mvvm.Command
	Connect, Disconnect, SignOut              *mvvm.Command
	ChooseTenant                              *mvvm.Command
	OpenSettings, SaveSettings, CloseSettings *mvvm.Command

	// can is what the model allows now, so commands re-evaluate with it.
	can *mvvm.Observable[appvm.Can]
	// options are the tenant rows last copied, to rebuild the list only when
	// they change.
	options []appvm.TenantOption
}

// Option configures a ViewModel.
type Option func(*config)

type config struct {
	post func(func())
	opts appvm.Options
}

// WithOpener replaces how a URL is opened (application.OpenURL in the app).
func WithOpener(open func(string) error) Option { return func(c *config) { c.opts.Open = open } }

// WithPost replaces how a result comes back to the UI thread: post runs a
// function there. The default runs it at once, which is right for a test that
// calls Apply and nothing else.
func WithPost(post func(func())) Option { return func(c *config) { c.post = post } }

// WithInterval sets the polling intervals (see appvm.Options).
func WithInterval(every, fast time.Duration) Option {
	return func(c *config) { c.opts.Interval, c.opts.FastInterval = every, fast }
}

// WithConfigPath names where the settings are saved, for the form to say.
func WithConfigPath(p string) Option { return func(c *config) { c.opts.ConfigPath = p } }

// NewViewModel builds the ViewModel over core. Call Start to begin polling.
func NewViewModel(core Core, opts ...Option) *ViewModel {
	c := config{post: func(f func()) { f() }}
	for _, o := range opts {
		o(&c)
	}
	m := appvm.New(core, c.opts)
	vm := &ViewModel{
		m: m, post: c.post,

		Page:    mvvm.NewObservable(PageStatus),
		State:   mvvm.NewObservable("Loading..."),
		Detail:  mvvm.NewObservable(""),
		Account: mvvm.NewObservable(""),
		Tenant:  mvvm.NewObservable(""),
		Helper:  mvvm.NewObservable(""),
		Busy:    mvvm.NewObservable(""),
		Message: mvvm.NewObservable(""),

		SignInPanel: mvvm.NewObservable(PanelHidden),
		DeviceURI:   mvvm.NewObservable(""),
		DeviceCode:  mvvm.NewObservable(""),

		TenantPanel: mvvm.NewObservable(PanelHidden),
		TenantNames: mvvm.NewObservableList[string](),
		TenantIndex: mvvm.NewObservable(0),

		Log: mvvm.NewObservableList[string](),

		ServerURL:      mvvm.NewObservable(""),
		GitHubClientID: mvvm.NewObservable(""),
		OIDCIssuer:     mvvm.NewObservable(""),
		OIDCClientID:   mvvm.NewObservable(""),
		ProviderIndex:  mvvm.NewObservable(0),
		ProviderPanel:  mvvm.NewObservable(ProviderGitHub),
		ConfigPath:     mvvm.NewObservable(c.opts.ConfigPath),

		TrayStatus: mvvm.NewObservable("Claimward"),
		can:        mvvm.NewObservable(appvm.Can{}),
	}
	vm.commands()
	vm.TenantIndex.SubscribeChanged(func() {
		if !vm.syncing {
			m.SelectTenant(vm.TenantIndex.Get())
		}
	})
	vm.ProviderIndex.Subscribe(func(i int) {
		if i >= 0 && i < len(Providers) && appvm.UsesIssuer(Providers[i].ID) {
			vm.ProviderPanel.Set(ProviderOIDC)
		} else {
			vm.ProviderPanel.Set(ProviderGitHub)
		}
	})
	vm.apply(m.State())
	// Subscriber callbacks run on any goroutine and must not touch the
	// observables: the state is carried to the UI thread.
	m.Subscribe(func(st appvm.State) { vm.post(func() { vm.apply(st) }) })
	return vm
}

func (vm *ViewModel) commands() {
	cmd := func(run func(), ok func(appvm.Can) bool) *mvvm.Command {
		c := mvvm.NewCommand(run, func() bool { return ok(vm.can.Get()) })
		mvvm.BindCanExecute(c, vm.can)
		return c
	}
	vm.SignIn = cmd(vm.m.SignIn, func(c appvm.Can) bool { return c.SignIn })
	vm.CancelSignIn = cmd(vm.m.CancelSignIn, func(c appvm.Can) bool { return c.CancelSignIn })
	vm.OpenSignInPage = cmd(vm.m.OpenSignInPage, func(c appvm.Can) bool { return c.OpenSignInPage })
	vm.ChooseTenant = cmd(vm.m.ChooseTenant, func(c appvm.Can) bool { return c.ChooseTenant })
	vm.Connect = cmd(vm.m.Connect, func(c appvm.Can) bool { return c.Connect })
	vm.Disconnect = cmd(vm.m.Disconnect, func(c appvm.Can) bool { return c.Disconnect })
	vm.SignOut = cmd(vm.m.SignOut, func(c appvm.Can) bool { return c.SignOut })
	vm.OpenSettings = cmd(vm.openSettings, func(c appvm.Can) bool { return c.OpenSettings })
	vm.SaveSettings = cmd(vm.saveSettings, func(c appvm.Can) bool { return c.SaveSettings })
	vm.CloseSettings = cmd(vm.m.CloseSettings, func(c appvm.Can) bool { return c.CloseSettings })
}

// openSettings fills the form from the configuration in use.
func (vm *ViewModel) openSettings() {
	f, ok := vm.m.OpenSettings()
	if !ok {
		return
	}
	vm.ServerURL.Set(f.ServerURL)
	vm.ProviderIndex.Set(appvm.ProviderIndex(f.Provider))
	vm.GitHubClientID.Set(f.GitHubClientID)
	vm.OIDCIssuer.Set(f.OIDCIssuer)
	vm.OIDCClientID.Set(f.OIDCClientID)
}

// saveSettings hands the form to the model, which saves it (an incomplete
// configuration too) and says what is wrong with it.
func (vm *ViewModel) saveSettings() {
	i := vm.ProviderIndex.Get()
	if i < 0 || i >= len(Providers) {
		i = 0
	}
	vm.m.SaveSettings(appvm.Form{
		ServerURL: vm.ServerURL.Get(), Provider: Providers[i].ID,
		GitHubClientID: vm.GitHubClientID.Get(),
		OIDCIssuer:     vm.OIDCIssuer.Get(), OIDCClientID: vm.OIDCClientID.Get(),
	})
}

// Model is the shared view model, for what the app does with its events.
func (vm *ViewModel) Model() *appvm.Model { return vm.m }

// Start polls the status until Stop. The first poll is immediate.
func (vm *ViewModel) Start() { vm.m.Start() }

// Stop ends the polling and cancels the action in progress.
func (vm *ViewModel) Stop() { vm.m.Stop() }

// Wait blocks until the poller and every action have finished and published
// their results. Results still have to be run on the UI thread.
func (vm *ViewModel) Wait() { vm.m.Wait() }

// Refresh reads the status once, as a poll does.
func (vm *ViewModel) Refresh() { vm.m.Refresh() }

// Apply shows a status, as a poll does.
func (vm *ViewModel) Apply(st appcore.Status) { vm.m.Apply(st) }

// apply copies a State into the observables. It runs on the UI thread.
func (vm *ViewModel) apply(st appvm.State) {
	vm.syncing = true
	defer func() { vm.syncing = false }()

	vm.can.Set(st.Can)
	if st.Screen == appvm.ScreenSettings {
		vm.Page.Set(PageSettings)
	} else {
		vm.Page.Set(PageStatus)
	}
	vm.State.Set(st.Headline)
	switch {
	case st.Screen == appvm.ScreenUnconfigured:
		vm.Detail.Set(st.Problem)
	case st.Connected:
		vm.Detail.Set(st.Address)
	default:
		vm.Detail.Set(st.Server)
	}
	vm.Account.Set(st.Account)
	vm.Helper.Set(st.Helper)
	vm.Tenant.Set(st.Tenant)
	vm.Busy.Set(st.Busy)
	if st.SettingsError != "" {
		vm.Message.Set(st.SettingsError)
	} else {
		vm.Message.Set(st.Error)
	}
	vm.ConfigPath.Set(st.ConfigPath)
	vm.TrayStatus.Set(st.TrayText)

	if st.Slot == appvm.SlotSignIn {
		vm.SignInPanel.Set(PanelShown)
	} else {
		vm.SignInPanel.Set(PanelHidden)
	}
	vm.DeviceURI.Set(st.SignInURL)
	vm.DeviceCode.Set(st.SignInCode)

	// The choice is made before connecting: once connected the tenant is the
	// session's, and the status line says which.
	if st.Slot == appvm.SlotTenants && !st.Connected {
		vm.TenantPanel.Set(PanelShown)
	} else {
		vm.TenantPanel.Set(PanelHidden)
	}
	if !sameOptions(vm.options, st.TenantOptions) {
		vm.options = append([]appvm.TenantOption(nil), st.TenantOptions...)
		rows := make([]string, len(vm.options))
		for i, o := range vm.options {
			rows[i] = o.Label
		}
		vm.TenantNames.Clear()
		vm.TenantNames.Append(rows...)
	}
	vm.TenantIndex.Set(st.TenantIndex)

	lines, appended := appvm.LogDelta(vm.Log.Slice(), st.Log)
	if !appended {
		vm.Log.Clear()
	}
	if len(lines) > 0 || !appended {
		vm.Log.Append(lines...)
	}
}

func sameOptions(a, b []appvm.TenantOption) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
