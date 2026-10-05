// SPDX-License-Identifier: BSD-3-Clause
//
// The ViewModel. It holds ALL of the window's and the tray's state as
// go-widgets/mvvm primitives and NEVER references a widget: the View
// (view.go) and the tray (tray_binding.go) bind to these, so state only
// ever changes here. It drives a Core -- appcore.Core in the app, a fake in
// the tests -- which does the work: sign-in, tenants, the helper.

package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/mvvm"
)

// Core is what the ViewModel needs of appcore.Core.
type Core interface {
	Status() appcore.Status
	Config() appcore.Config
	UpdateConfig(appcore.Config) error
	Login(ctx context.Context) error
	Tenants(ctx context.Context) ([]hproto.Tenant, error)
	SetTenant(id string) error
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Logout(ctx context.Context) error
}

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

// Providers are the identity providers offered, in DropDown order, with the
// names appcore.Config uses for them.
var Providers = []struct{ ID, Label string }{
	{"github", "GitHub (device flow)"},
	{"oidc", "OpenID Connect"},
	{"go-authn", "go-authn (OIDC, registers the device key)"},
}

// chooseTenant is the first row of the tenant DropDown: "no choice made".
const chooseTenant = "Choose a tenant..."

// ViewModel is the application state.
type ViewModel struct {
	core Core
	// open shows a URL in the default browser.
	open func(string) error
	// spawn runs blocking work (the Core talks to the helper and the
	// network) away from the UI thread; post brings its result back to the
	// UI thread, the only one that touches an observable.
	spawn, post func(func())

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
	// DropDown's rows (chooseTenant first) and TenantIndex the selected one.
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

	// rev changes with every status applied, so commands re-evaluate.
	rev *mvvm.Observable[int]
	// st is the last status applied, and tenants the ones it offered, in
	// TenantNames order (after chooseTenant).
	st      appcore.Status
	tenants []hproto.Tenant
	// syncing is set while applyStatus writes TenantIndex, so that what the
	// server says is not taken for the person choosing.
	syncing bool
	// refreshing is set while a status read is under way.
	refreshing bool
	// cancelLogin stops a sign-in that waits for the person.
	cancelLogin context.CancelFunc
}

// Option configures a ViewModel.
type Option func(*ViewModel)

// WithOpener replaces how a URL is opened (the default browser by default).
func WithOpener(open func(string) error) Option { return func(vm *ViewModel) { vm.open = open } }

// WithDispatch replaces how work leaves and comes back to the UI thread:
// spawn runs a blocking call, post runs a function on the UI thread. The
// default runs both inline, which is right for a test and nothing else.
func WithDispatch(spawn, post func(func())) Option {
	return func(vm *ViewModel) { vm.spawn, vm.post = spawn, post }
}

// WithConfigPath names where the settings are saved, for the form to say.
func WithConfigPath(p string) Option { return func(vm *ViewModel) { vm.ConfigPath.Set(p) } }

// NewViewModel builds the ViewModel over core. Call Refresh (or
// StartPolling) to read the first status.
func NewViewModel(core Core, opts ...Option) *ViewModel {
	inline := func(f func()) { f() }
	vm := &ViewModel{
		core:  core,
		open:  func(string) error { return errors.New("no browser") },
		spawn: inline, post: inline,

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
		TenantNames: mvvm.NewObservableList(chooseTenant),
		TenantIndex: mvvm.NewObservable(0),

		Log: mvvm.NewObservableList[string](),

		ServerURL:      mvvm.NewObservable(""),
		GitHubClientID: mvvm.NewObservable(""),
		OIDCIssuer:     mvvm.NewObservable(""),
		OIDCClientID:   mvvm.NewObservable(""),
		ProviderIndex:  mvvm.NewObservable(0),
		ProviderPanel:  mvvm.NewObservable(ProviderGitHub),
		ConfigPath:     mvvm.NewObservable(""),

		TrayStatus: mvvm.NewObservable("Claimward"),
		rev:        mvvm.NewObservable(0),
	}
	for _, o := range opts {
		o(vm)
	}

	idle := func() bool { return vm.Busy.Get() == "" }
	onStatus := func() bool { return vm.Page.Get() == PageStatus }
	vm.SignIn = mvvm.NewCommand(vm.signIn, func() bool {
		return onStatus() && idle() && vm.st.ConfigOK && !vm.st.LoggedIn
	})
	vm.CancelSignIn = mvvm.NewCommand(vm.cancelSignIn, func() bool { return vm.cancelLogin != nil })
	vm.OpenSignInPage = mvvm.NewCommand(vm.openSignInPage, func() bool { return vm.DeviceURI.Get() != "" })
	vm.Connect = mvvm.NewCommand(vm.connect, func() bool {
		return idle() && vm.st.ConfigOK && vm.st.LoggedIn && !vm.st.Connected
	})
	vm.Disconnect = mvvm.NewCommand(vm.disconnect, func() bool { return idle() && vm.st.Connected })
	vm.SignOut = mvvm.NewCommand(vm.signOut, func() bool { return onStatus() && idle() && vm.st.LoggedIn })
	vm.ChooseTenant = mvvm.NewCommand(vm.chooseTenant, func() bool {
		return onStatus() && idle() && vm.st.ConfigOK && vm.st.LoggedIn && !vm.st.Connected
	})
	vm.OpenSettings = mvvm.NewCommand(vm.openSettings, func() bool { return onStatus() })
	vm.SaveSettings = mvvm.NewCommand(vm.saveSettings, func() bool { return !onStatus() && idle() })
	vm.CloseSettings = mvvm.NewCommand(func() { vm.Page.Set(PageStatus) }, func() bool { return !onStatus() })
	for _, c := range []*mvvm.Command{vm.SignIn, vm.CancelSignIn, vm.OpenSignInPage, vm.Connect, vm.Disconnect,
		vm.SignOut, vm.ChooseTenant, vm.OpenSettings, vm.SaveSettings, vm.CloseSettings} {
		mvvm.BindCanExecute(c, vm.Busy, vm.rev, vm.Page, vm.DeviceURI)
	}

	vm.TenantIndex.SubscribeChanged(vm.tenantChosen)
	vm.ProviderIndex.Subscribe(func(i int) {
		if i == 0 {
			vm.ProviderPanel.Set(ProviderGitHub)
		} else {
			vm.ProviderPanel.Set(ProviderOIDC)
		}
	})
	return vm
}

// Refresh reads the Core's status away from the UI thread and applies it.
// One read at a time: a poll that comes while one is under way is dropped.
func (vm *ViewModel) Refresh() {
	if vm.refreshing {
		return
	}
	vm.refreshing = true
	vm.spawn(func() {
		st := vm.core.Status()
		vm.post(func() {
			vm.refreshing = false
			vm.applyStatus(st)
		})
	})
}

// StartPolling refreshes now and then every interval until ctx ends. The
// ticker posts to the UI thread; Refresh does the rest.
func (vm *ViewModel) StartPolling(ctx context.Context, every time.Duration) {
	vm.post(vm.Refresh)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				vm.post(vm.Refresh)
			}
		}
	}()
}

// applyStatus turns a Core status into the observables. It is the only
// writer of the status-derived state.
func (vm *ViewModel) applyStatus(st appcore.Status) {
	vm.st = st
	switch {
	case !st.ConfigOK:
		vm.State.Set("Not configured")
		vm.Detail.Set(st.ConfigError)
	case st.Connected:
		vm.State.Set("Connected")
		vm.Detail.Set(joinNonEmpty(" - ", st.AssignedIP, st.Interface))
	case st.LoggedIn:
		vm.State.Set("Disconnected")
		vm.Detail.Set(st.ServerURL)
	default:
		vm.State.Set("Signed out")
		vm.Detail.Set(st.ServerURL)
	}

	switch {
	case st.LoggedIn && st.Email != "":
		vm.Account.Set("Signed in as " + st.Email)
	case st.LoggedIn:
		vm.Account.Set("Signed in")
	default:
		vm.Account.Set("")
	}
	if st.HelperInstalled {
		vm.Helper.Set("Helper service: running")
	} else {
		vm.Helper.Set("Helper service not reachable: install it (see the README) or start ClaimwardHelper")
	}

	if st.DeviceVerificationURI != "" {
		vm.DeviceURI.Set(st.DeviceVerificationURI)
		vm.DeviceCode.Set(st.DeviceUserCode)
		vm.SignInPanel.Set(PanelShown)
	} else {
		vm.SignInPanel.Set(PanelHidden)
		vm.DeviceURI.Set("")
		vm.DeviceCode.Set("")
	}

	vm.applyTenants(st)
	vm.applyLog(st.Log)

	tray := "Claimward: " + vm.State.Get()
	if st.Connected && st.AssignedIP != "" {
		tray += " (" + st.AssignedIP + ")"
	}
	vm.TrayStatus.Set(tray)
	vm.rev.Set(vm.rev.Get() + 1)
}

// applyTenants shows the choice when there is one to make: several tenants
// offered, or a connection refused until one is chosen. A person in one
// tenant never chooses; the server uses it.
func (vm *ViewModel) applyTenants(st appcore.Status) {
	name := st.Tenant
	for _, t := range st.Tenants {
		if t.ID == st.Tenant && t.Name != "" {
			name = t.Name
		}
	}
	if name != "" {
		vm.Tenant.Set("Tenant: " + name)
	} else {
		vm.Tenant.Set("")
	}

	show := st.LoggedIn && !st.Connected && (len(st.Tenants) > 1 || st.TenantRequired)
	if !sameTenants(vm.tenants, st.Tenants) {
		vm.tenants = append([]hproto.Tenant(nil), st.Tenants...)
		rows := []string{chooseTenant}
		for _, t := range vm.tenants {
			rows = append(rows, tenantLabel(t))
		}
		vm.syncing = true
		vm.TenantNames.Clear()
		vm.TenantNames.Append(rows...)
		vm.syncing = false
	}
	idx := 0
	for i, t := range vm.tenants {
		if t.ID == st.Tenant {
			idx = i + 1
		}
	}
	vm.syncing = true
	vm.TenantIndex.Set(idx)
	vm.syncing = false
	if show {
		vm.TenantPanel.Set(PanelShown)
	} else {
		vm.TenantPanel.Set(PanelHidden)
	}
}

func (vm *ViewModel) applyLog(lines []string) {
	if len(lines) == vm.Log.Len() && (len(lines) == 0 || lines[len(lines)-1] == vm.Log.At(vm.Log.Len()-1)) {
		return
	}
	vm.Log.Clear()
	vm.Log.Append(lines...)
}

// tenantChosen is the person picking a row of the tenant DropDown.
func (vm *ViewModel) tenantChosen() {
	if vm.syncing {
		return
	}
	i := vm.TenantIndex.Get()
	id := ""
	if i > 0 && i <= len(vm.tenants) {
		id = vm.tenants[i-1].ID
	}
	vm.run("Choosing the tenant...", func() error { return vm.core.SetTenant(id) }, nil)
}

// run does work away from the UI thread with Busy set; back on the UI
// thread it calls after (when given), clears Busy, reports the error and
// refreshes.
func (vm *ViewModel) run(busy string, work func() error, after func(error)) {
	vm.Busy.Set(busy)
	vm.Message.Set("")
	vm.spawn(func() {
		err := work()
		vm.post(func() {
			if after != nil {
				after(err)
			}
			vm.Busy.Set("")
			if err != nil {
				vm.Message.Set(describe(err))
			}
			vm.Refresh()
		})
	})
}

// describe is an error as the person reads it.
func describe(err error) string {
	switch {
	case errors.Is(err, appcore.ErrTenantRequired):
		return "You belong to several tenants: choose one, then Connect."
	case errors.Is(err, context.Canceled):
		return "Sign-in cancelled."
	}
	return err.Error()
}

func (vm *ViewModel) signIn() {
	ctx, cancel := context.WithCancel(context.Background())
	vm.cancelLogin = cancel
	vm.run("Signing in...", func() error { return vm.core.Login(ctx) }, func(error) {
		cancel()
		vm.cancelLogin = nil
	})
}

func (vm *ViewModel) cancelSignIn() {
	if vm.cancelLogin != nil {
		vm.cancelLogin()
	}
}

// openSignInPage opens the device-flow page. Only an https URL is opened:
// it came from the identity provider through the network, and on Windows a
// URL handed to the shell can as well name a program to run.
func (vm *ViewModel) openSignInPage() {
	raw := vm.DeviceURI.Get()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		vm.Message.Set(fmt.Sprintf("Not opening %q: the sign-in page must be an https URL. Open it yourself if you trust it.", raw))
		return
	}
	if err := vm.open(u.String()); err != nil {
		vm.Message.Set("Could not open the browser: " + err.Error() + ". Open " + u.String() + " yourself.")
	}
}

func (vm *ViewModel) connect() {
	vm.run("Connecting...", func() error { return vm.core.Connect(context.Background()) }, nil)
}

func (vm *ViewModel) disconnect() {
	vm.run("Disconnecting...", func() error { return vm.core.Disconnect(context.Background()) }, nil)
}

func (vm *ViewModel) signOut() {
	vm.run("Signing out...", func() error { return vm.core.Logout(context.Background()) }, nil)
}

// chooseTenant asks the server which tenants the person may join. Several
// show the choice (applyTenants); one, or none, is said so.
func (vm *ViewModel) chooseTenant() {
	var n int
	vm.run("Asking the server for your tenants...", func() error {
		ts, err := vm.core.Tenants(context.Background())
		n = len(ts)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		switch n {
		case 0:
			vm.Message.Set("The server lists no tenant for you.")
		case 1:
			vm.Message.Set("You belong to one tenant: there is nothing to choose.")
		}
	})
}

// openSettings fills the form from the configuration in use.
func (vm *ViewModel) openSettings() {
	c := vm.core.Config()
	vm.ServerURL.Set(c.ServerURL)
	vm.GitHubClientID.Set(c.GitHubClientID)
	vm.OIDCIssuer.Set(c.OIDCIssuer)
	vm.OIDCClientID.Set(c.OIDCClientID)
	idx := 0
	for i, p := range Providers {
		if p.ID == c.Provider {
			idx = i
		}
	}
	vm.ProviderIndex.Set(idx)
	vm.Message.Set("")
	vm.Page.Set(PageSettings)
}

// saveSettings saves the form. An incomplete configuration is saved all
// the same -- the person may be filling it in over two sittings -- and the
// status page says what it lacks.
func (vm *ViewModel) saveSettings() {
	c := vm.core.Config()
	c.ServerURL = strings.TrimSpace(vm.ServerURL.Get())
	c.GitHubClientID = strings.TrimSpace(vm.GitHubClientID.Get())
	c.OIDCIssuer = strings.TrimSpace(vm.OIDCIssuer.Get())
	c.OIDCClientID = strings.TrimSpace(vm.OIDCClientID.Get())
	c.Provider = Providers[0].ID
	if i := vm.ProviderIndex.Get(); i >= 0 && i < len(Providers) {
		c.Provider = Providers[i].ID
	}
	if c.ServerURL != "" {
		if u, err := url.Parse(c.ServerURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			vm.Message.Set("The server URL must be an http(s) URL, such as https://vpn.example.org.")
			return
		}
	}
	vm.run("Saving...", func() error { return vm.core.UpdateConfig(c) }, func(err error) {
		if err != nil {
			return
		}
		vm.Page.Set(PageStatus)
		if err := c.Validate(); err != nil {
			vm.Message.Set("Saved, but not complete: " + err.Error())
		}
	})
}

func sameTenants(a, b []hproto.Tenant) bool {
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

func tenantLabel(t hproto.Tenant) string {
	if t.Name == "" || t.Name == t.ID {
		return t.ID
	}
	return t.Name + " (" + t.ID + ")"
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
