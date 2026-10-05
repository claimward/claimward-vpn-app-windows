// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
)

// fakeCore behaves as appcore.Core does as far as the ViewModel can see.
type fakeCore struct {
	mu  sync.Mutex
	st  appcore.Status
	cfg appcore.Config

	// offered are the tenants the server lists.
	offered []hproto.Tenant

	loginErr, connectErr, tenantsErr, setTenantErr, updateErr, disconnectErr, logoutErr error
	// loginWait, when set, makes Login wait for it or for its context.
	loginWait chan struct{}

	calls     []string
	setTenant []string
	saved     []appcore.Config
	statuses  int
}

func (f *fakeCore) call(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeCore) Status() appcore.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses++
	return f.st
}

func (f *fakeCore) Config() appcore.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg
}

func (f *fakeCore) UpdateConfig(c appcore.Config) error {
	f.call("UpdateConfig")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.saved = append(f.saved, c)
	f.cfg = c
	f.st.ServerURL = c.ServerURL
	f.st.ConfigOK = c.Validate() == nil
	return nil
}

func (f *fakeCore) Login(ctx context.Context) error {
	f.call("Login")
	if f.loginWait != nil {
		select {
		case <-f.loginWait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginErr != nil {
		return f.loginErr
	}
	f.st.LoggedIn, f.st.Email = true, "ada@example.org"
	f.st.DeviceVerificationURI, f.st.DeviceUserCode = "", ""
	return nil
}

func (f *fakeCore) Tenants(context.Context) ([]hproto.Tenant, error) {
	f.call("Tenants")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tenantsErr != nil {
		return nil, f.tenantsErr
	}
	f.st.Tenants = append([]hproto.Tenant(nil), f.offered...)
	return f.offered, nil
}

func (f *fakeCore) SetTenant(id string) error {
	f.call("SetTenant")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setTenant = append(f.setTenant, id)
	if f.setTenantErr != nil {
		return f.setTenantErr
	}
	f.st.Tenant, f.st.TenantRequired = id, false
	return nil
}

// Connect refuses a person in several tenants who chose none, as the
// server and appcore do, offering the tenants.
func (f *fakeCore) Connect(context.Context) error {
	f.call("Connect")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connectErr != nil {
		return f.connectErr
	}
	if len(f.offered) > 1 && f.st.Tenant == "" {
		f.st.Tenants, f.st.TenantRequired = append([]hproto.Tenant(nil), f.offered...), true
		return appcore.ErrTenantRequired
	}
	f.st.Connected, f.st.AssignedIP, f.st.Interface = true, "10.80.0.7/32", "Claimward"
	return nil
}

func (f *fakeCore) Disconnect(context.Context) error {
	f.call("Disconnect")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st.Connected, f.st.AssignedIP, f.st.Interface = false, "", ""
	return f.disconnectErr
}

func (f *fakeCore) Logout(context.Context) error {
	f.call("Logout")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st = appcore.Status{ConfigOK: f.st.ConfigOK, ServerURL: f.st.ServerURL, HelperInstalled: f.st.HelperInstalled}
	return f.logoutErr
}

func (f *fakeCore) set(fn func(st *appcore.Status)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.st)
}

func (f *fakeCore) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

var goodConfig = appcore.Config{ServerURL: "https://vpn.example.org", Provider: "github", GitHubClientID: "Iv1.abc", SocketPath: `C:\custom\helper.sock`}

// signedOut is a configured core, helper running, nobody signed in.
func signedOut() *fakeCore {
	return &fakeCore{
		cfg: goodConfig,
		st:  appcore.Status{ConfigOK: true, ServerURL: goodConfig.ServerURL, Provider: "github", HelperInstalled: true},
	}
}

func signedIn() *fakeCore {
	f := signedOut()
	f.st.LoggedIn, f.st.Email = true, "ada@example.org"
	return f
}

// newVM is a ViewModel whose work runs inline, refreshed once.
func newVM(t *testing.T, f *fakeCore, opts ...Option) *ViewModel {
	t.Helper()
	vm := NewViewModel(f, opts...)
	vm.Refresh()
	return vm
}

type commandState map[string]bool

func commands(vm *ViewModel) commandState {
	return commandState{
		"SignIn": vm.SignIn.CanExecute(), "Connect": vm.Connect.CanExecute(), "Disconnect": vm.Disconnect.CanExecute(),
		"SignOut": vm.SignOut.CanExecute(), "ChooseTenant": vm.ChooseTenant.CanExecute(),
	}
}

func TestStatusLines(t *testing.T) {
	f := signedOut()
	vm := NewViewModel(f)
	if vm.State.Get() != "Loading..." {
		t.Errorf("before the first status: %q", vm.State.Get())
	}
	vm.Refresh()
	if vm.State.Get() != "Signed out" || vm.Detail.Get() != goodConfig.ServerURL || vm.Account.Get() != "" {
		t.Errorf("signed out: %q %q %q", vm.State.Get(), vm.Detail.Get(), vm.Account.Get())
	}
	if !strings.Contains(vm.Helper.Get(), "running") {
		t.Errorf("helper: %q", vm.Helper.Get())
	}

	f.set(func(st *appcore.Status) { st.LoggedIn, st.Email = true, "ada@example.org" })
	vm.Refresh()
	if vm.State.Get() != "Disconnected" || vm.Account.Get() != "Signed in as ada@example.org" {
		t.Errorf("signed in: %q %q", vm.State.Get(), vm.Account.Get())
	}
	f.set(func(st *appcore.Status) { st.Email = "" })
	vm.Refresh()
	if vm.Account.Get() != "Signed in" {
		t.Errorf("signed in, token without a name: %q", vm.Account.Get())
	}

	f.set(func(st *appcore.Status) {
		st.Connected, st.AssignedIP, st.Interface, st.Tenant = true, "10.80.0.7/32", "Claimward", "chem"
		st.Tenants = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}, {ID: "hpc"}}
	})
	vm.Refresh()
	if vm.State.Get() != "Connected" || vm.Detail.Get() != "10.80.0.7/32 - Claimward" || vm.Tenant.Get() != "Tenant: Chemistry" {
		t.Errorf("connected: %q %q %q", vm.State.Get(), vm.Detail.Get(), vm.Tenant.Get())
	}
	if vm.TrayStatus.Get() != "Claimward: Connected (10.80.0.7/32)" {
		t.Errorf("tray: %q", vm.TrayStatus.Get())
	}

	f.set(func(st *appcore.Status) { st.Tenant, st.Tenants = "hpc", nil })
	vm.Refresh()
	if vm.Tenant.Get() != "Tenant: hpc" {
		t.Errorf("a tenant with no name is shown by id: %q", vm.Tenant.Get())
	}

	f.set(func(st *appcore.Status) { st.HelperInstalled = false })
	vm.Refresh()
	if !strings.Contains(vm.Helper.Get(), "not reachable") {
		t.Errorf("helper missing: %q", vm.Helper.Get())
	}

	f.set(func(st *appcore.Status) { *st = appcore.Status{ConfigError: "missing config: server_url"} })
	vm.Refresh()
	if vm.State.Get() != "Not configured" || vm.Detail.Get() != "missing config: server_url" {
		t.Errorf("unconfigured: %q %q", vm.State.Get(), vm.Detail.Get())
	}
	if vm.TrayStatus.Get() != "Claimward: Not configured" {
		t.Errorf("tray: %q", vm.TrayStatus.Get())
	}
}

// Each state offers what can be done in it and nothing else.
func TestCommandsFollowTheState(t *testing.T) {
	f := &fakeCore{st: appcore.Status{ConfigError: "missing"}}
	vm := newVM(t, f)
	if got := commands(vm); !reflect.DeepEqual(got, commandState{"SignIn": false, "Connect": false, "Disconnect": false, "SignOut": false, "ChooseTenant": false}) {
		t.Errorf("unconfigured: %v", got)
	}
	f.set(func(st *appcore.Status) { *st = signedOut().st })
	vm.Refresh()
	if got := commands(vm); !reflect.DeepEqual(got, commandState{"SignIn": true, "Connect": false, "Disconnect": false, "SignOut": false, "ChooseTenant": false}) {
		t.Errorf("signed out: %v", got)
	}
	f.set(func(st *appcore.Status) { st.LoggedIn = true })
	vm.Refresh()
	if got := commands(vm); !reflect.DeepEqual(got, commandState{"SignIn": false, "Connect": true, "Disconnect": false, "SignOut": true, "ChooseTenant": true}) {
		t.Errorf("signed in: %v", got)
	}
	f.set(func(st *appcore.Status) { st.Connected = true })
	vm.Refresh()
	if got := commands(vm); !reflect.DeepEqual(got, commandState{"SignIn": false, "Connect": false, "Disconnect": true, "SignOut": true, "ChooseTenant": false}) {
		t.Errorf("connected: %v", got)
	}
}

// manual is a dispatcher a test steps through: spawned work waits until the
// test runs it, and posted results go through a Queue the test drains.
type manual struct {
	q    Queue
	work []func()
}

func (m *manual) spawn(f func()) { m.work = append(m.work, f) }

func (m *manual) runWork() {
	w := m.work
	m.work = nil
	for _, f := range w {
		f()
	}
	m.q.Drain()
}

// While something runs, nothing else may start, and Busy says what runs.
func TestBusyHoldsTheOtherCommands(t *testing.T) {
	f := signedIn()
	m := &manual{}
	vm := NewViewModel(f, WithDispatch(m.spawn, m.q.Post))
	vm.Refresh()
	m.runWork()
	vm.Connect.Execute()
	if vm.Busy.Get() != "Connecting..." {
		t.Errorf("busy %q", vm.Busy.Get())
	}
	if got := commands(vm); got["Connect"] || got["SignOut"] || got["ChooseTenant"] {
		t.Errorf("while connecting: %v", got)
	}
	vm.Connect.Execute() // a second click is not a second connection
	m.runWork()
	m.runWork() // the refresh the end of the work asked for
	if f.called("Connect") != 1 {
		t.Errorf("Connect called %d times", f.called("Connect"))
	}
	if vm.Busy.Get() != "" || vm.State.Get() != "Connected" || !vm.Disconnect.CanExecute() {
		t.Errorf("after: busy %q state %q", vm.Busy.Get(), vm.State.Get())
	}
	// A poll while a status read is under way is dropped.
	vm.Refresh()
	vm.Refresh()
	if len(m.work) != 1 {
		t.Errorf("%d status reads queued, want 1", len(m.work))
	}
}

func TestSignInWithTheDeviceFlow(t *testing.T) {
	f := signedOut()
	f.loginWait = make(chan struct{})
	q := &Queue{}
	var opened []string
	vm := NewViewModel(f,
		WithDispatch(func(fn func()) { go fn() }, q.Post),
		WithOpener(func(u string) error { opened = append(opened, u); return nil }))
	refresh := func() {
		t.Helper()
		vm.Refresh()
		waitFor(t, q, func() bool { return !vm.refreshing })
	}
	refresh()
	if vm.SignInPanel.Get() != PanelHidden || vm.OpenSignInPage.CanExecute() {
		t.Error("the device prompt shows before a sign-in")
	}
	vm.SignIn.Execute()
	if vm.Busy.Get() != "Signing in..." || !vm.CancelSignIn.CanExecute() || vm.SignIn.CanExecute() {
		t.Errorf("signing in: busy %q", vm.Busy.Get())
	}
	// The provider answers with a code to type at a page.
	f.set(func(st *appcore.Status) {
		st.DeviceVerificationURI, st.DeviceUserCode = "https://github.com/login/device", "WDJB-MJHT"
	})
	refresh()
	if vm.SignInPanel.Get() != PanelShown || vm.DeviceCode.Get() != "WDJB-MJHT" || vm.DeviceURI.Get() != "https://github.com/login/device" {
		t.Errorf("prompt: panel %d code %q uri %q", vm.SignInPanel.Get(), vm.DeviceCode.Get(), vm.DeviceURI.Get())
	}
	vm.OpenSignInPage.Execute()
	if !reflect.DeepEqual(opened, []string{"https://github.com/login/device"}) {
		t.Errorf("opened %v", opened)
	}
	close(f.loginWait)
	waitFor(t, q, func() bool { return vm.Busy.Get() == "" && vm.State.Get() == "Disconnected" })
	if vm.SignInPanel.Get() != PanelHidden || vm.CancelSignIn.CanExecute() || vm.Message.Get() != "" {
		t.Errorf("after: panel %d cancel %v message %q", vm.SignInPanel.Get(), vm.CancelSignIn.CanExecute(), vm.Message.Get())
	}
}

func TestCancelSignIn(t *testing.T) {
	f := signedOut()
	f.loginWait = make(chan struct{})
	q := &Queue{}
	vm := NewViewModel(f, WithDispatch(func(fn func()) { go fn() }, q.Post))
	vm.Refresh()
	waitFor(t, q, func() bool { return vm.SignIn.CanExecute() })
	vm.SignIn.Execute()
	vm.CancelSignIn.Execute()
	waitFor(t, q, func() bool { return vm.Busy.Get() == "" })
	if vm.Message.Get() != "Sign-in cancelled." || vm.State.Get() != "Signed out" || vm.CancelSignIn.CanExecute() {
		t.Errorf("message %q state %q", vm.Message.Get(), vm.State.Get())
	}
	vm.CancelSignIn.Execute() // nothing to cancel: no effect
}

func TestSignInFailure(t *testing.T) {
	f := signedOut()
	f.loginErr = errors.New("device flow: access_denied")
	vm := newVM(t, f)
	vm.SignIn.Execute()
	if vm.Message.Get() != "device flow: access_denied" || vm.State.Get() != "Signed out" {
		t.Errorf("message %q state %q", vm.Message.Get(), vm.State.Get())
	}
}

// Only an https page is handed to the shell: on Windows a "URL" can name a
// program to run.
func TestOnlyAnHTTPSSignInPageIsOpened(t *testing.T) {
	for _, uri := range []string{"http://github.com/login/device", `C:\Windows\System32\calc.exe`, "file:///C:/x.exe",
		"javascript:alert(1)", "https://", "ms-settings:network"} {
		f := signedOut()
		f.st.DeviceVerificationURI = uri
		opened := 0
		vm := newVM(t, f, WithOpener(func(string) error { opened++; return nil }))
		vm.OpenSignInPage.Execute()
		if opened != 0 || !strings.Contains(vm.Message.Get(), "https") {
			t.Errorf("%q: opened %d times, message %q", uri, opened, vm.Message.Get())
		}
	}
	f := signedOut()
	f.st.DeviceVerificationURI = "https://login.example.org/device"
	vm := newVM(t, f, WithOpener(func(string) error { return errors.New("no default browser") }))
	vm.OpenSignInPage.Execute()
	if !strings.Contains(vm.Message.Get(), "no default browser") || !strings.Contains(vm.Message.Get(), "https://login.example.org/device") {
		t.Errorf("a browser that fails: %q", vm.Message.Get())
	}
}

// A person in several tenants who chose none is told to choose; the choice
// is offered, made, and the next connection uses it.
func TestConnectRefusedUntilATenantIsChosen(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}, {ID: "hpc", Name: "HPC"}}
	vm := newVM(t, f)
	if vm.TenantPanel.Get() != PanelHidden {
		t.Error("the choice shows before anything was offered")
	}
	vm.Connect.Execute()
	if vm.Message.Get() != "You belong to several tenants: choose one, then Connect." {
		t.Errorf("message %q", vm.Message.Get())
	}
	if vm.TenantPanel.Get() != PanelShown {
		t.Fatal("the tenant choice is not shown")
	}
	if got := vm.TenantNames.Slice(); !reflect.DeepEqual(got, []string{chooseTenant, "Chemistry (chem)", "HPC (hpc)"}) {
		t.Errorf("rows %v", got)
	}
	if vm.TenantIndex.Get() != 0 || f.called("SetTenant") != 0 {
		t.Errorf("index %d, SetTenant called %d times before any choice", vm.TenantIndex.Get(), f.called("SetTenant"))
	}

	vm.TenantIndex.Set(2) // the person picks HPC
	if !reflect.DeepEqual(f.setTenant, []string{"hpc"}) {
		t.Errorf("SetTenant %v", f.setTenant)
	}
	if vm.Tenant.Get() != "Tenant: HPC" || vm.TenantIndex.Get() != 2 {
		t.Errorf("tenant %q index %d", vm.Tenant.Get(), vm.TenantIndex.Get())
	}
	// Refreshing re-applies the same choice without calling SetTenant.
	vm.Refresh()
	vm.Refresh()
	if f.called("SetTenant") != 1 {
		t.Errorf("SetTenant called %d times", f.called("SetTenant"))
	}
	vm.Connect.Execute()
	if vm.State.Get() != "Connected" || vm.Message.Get() != "" {
		t.Errorf("state %q message %q", vm.State.Get(), vm.Message.Get())
	}
	if vm.TenantPanel.Get() != PanelHidden {
		t.Error("the choice stays open while connected")
	}

	vm.Disconnect.Execute()
	vm.TenantIndex.Set(0) // back to "the server's choice"
	if got := f.setTenant[len(f.setTenant)-1]; got != "" {
		t.Errorf("choosing the first row set tenant %q", got)
	}
}

func TestATenantChoiceTheCoreRefuses(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem"}, {ID: "hpc"}}
	vm := newVM(t, f)
	vm.ChooseTenant.Execute()
	f.setTenantErr = errors.New(`"hpc" is not a tenant you were offered`)
	vm.TenantIndex.Set(2)
	if !strings.Contains(vm.Message.Get(), "not a tenant you were offered") {
		t.Errorf("message %q", vm.Message.Get())
	}
	// The status puts the DropDown back on what is actually chosen.
	if vm.TenantIndex.Get() != 0 {
		t.Errorf("index %d after a refused choice", vm.TenantIndex.Get())
	}
}

// "Choose tenant" asks the server; a person in one tenant never chooses.
func TestChooseTenant(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}}
	vm := newVM(t, f)
	vm.ChooseTenant.Execute()
	if f.called("Tenants") != 1 || vm.TenantPanel.Get() != PanelHidden ||
		vm.Message.Get() != "You belong to one tenant: there is nothing to choose." {
		t.Errorf("one tenant: panel %d message %q", vm.TenantPanel.Get(), vm.Message.Get())
	}

	f.offered = nil
	vm.ChooseTenant.Execute()
	if vm.Message.Get() != "The server lists no tenant for you." {
		t.Errorf("no tenant: %q", vm.Message.Get())
	}

	f.offered = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}, {ID: "hpc", Name: "hpc"}}
	vm.ChooseTenant.Execute()
	if vm.TenantPanel.Get() != PanelShown || vm.Message.Get() != "" {
		t.Errorf("two tenants: panel %d message %q", vm.TenantPanel.Get(), vm.Message.Get())
	}
	if got := vm.TenantNames.Slice(); !reflect.DeepEqual(got, []string{chooseTenant, "Chemistry (chem)", "hpc"}) {
		t.Errorf("rows %v", got)
	}

	f.tenantsErr = errors.New("helper: server unreachable")
	vm.ChooseTenant.Execute()
	if vm.Message.Get() != "helper: server unreachable" {
		t.Errorf("error: %q", vm.Message.Get())
	}
}

func TestConnectErrors(t *testing.T) {
	f := signedIn()
	f.connectErr = errors.New(`enroll: server "https://evil.example" is not one this helper is configured for`)
	vm := newVM(t, f)
	vm.Connect.Execute()
	if vm.Message.Get() != f.connectErr.Error() || vm.State.Get() != "Disconnected" || !vm.Connect.CanExecute() {
		t.Errorf("message %q state %q", vm.Message.Get(), vm.State.Get())
	}
	// The core's own ErrTenantRequired, wrapped, reads the same.
	f.connectErr = errors.Join(errors.New("connect"), appcore.ErrTenantRequired)
	vm.Connect.Execute()
	if !strings.HasPrefix(vm.Message.Get(), "You belong to several tenants") {
		t.Errorf("wrapped ErrTenantRequired: %q", vm.Message.Get())
	}
	// A new action clears the last message.
	f.connectErr = nil
	vm.Connect.Execute()
	if vm.Message.Get() != "" || vm.State.Get() != "Connected" {
		t.Errorf("message %q state %q", vm.Message.Get(), vm.State.Get())
	}
	f.disconnectErr = errors.New("helper: no answer")
	vm.Disconnect.Execute()
	if vm.Message.Get() != "helper: no answer" {
		t.Errorf("disconnect error %q", vm.Message.Get())
	}
}

func TestSignOut(t *testing.T) {
	f := signedIn()
	vm := newVM(t, f)
	vm.SignOut.Execute()
	if f.called("Logout") != 1 || vm.State.Get() != "Signed out" || vm.Account.Get() != "" || !vm.SignIn.CanExecute() {
		t.Errorf("state %q account %q", vm.State.Get(), vm.Account.Get())
	}
}

func TestSettings(t *testing.T) {
	f := signedOut()
	f.cfg.Provider = "go-authn"
	f.cfg.OIDCIssuer, f.cfg.OIDCClientID = "https://login.example.org", "claimward"
	vm := newVM(t, f, WithConfigPath(`Saved to C:\Users\ada\AppData\Roaming\Claimward\config.json`))
	if vm.SaveSettings.CanExecute() || vm.CloseSettings.CanExecute() {
		t.Error("saving is offered off the settings page")
	}
	vm.OpenSettings.Execute()
	if vm.Page.Get() != PageSettings || vm.ServerURL.Get() != goodConfig.ServerURL || vm.ProviderIndex.Get() != 2 ||
		vm.ProviderPanel.Get() != ProviderOIDC || vm.OIDCIssuer.Get() != "https://login.example.org" || vm.OIDCClientID.Get() != "claimward" {
		t.Errorf("form: page %d server %q provider %d panel %d", vm.Page.Get(), vm.ServerURL.Get(), vm.ProviderIndex.Get(), vm.ProviderPanel.Get())
	}
	if vm.SignIn.CanExecute() || vm.OpenSettings.CanExecute() {
		t.Error("status commands are offered on the settings page")
	}
	vm.ProviderIndex.Set(0)
	if vm.ProviderPanel.Get() != ProviderGitHub {
		t.Error("choosing GitHub does not show the GitHub field")
	}
	vm.ServerURL.Set("  https://vpn2.example.org/ ")
	vm.GitHubClientID.Set(" Iv1.new ")
	vm.SaveSettings.Execute()
	if len(f.saved) != 1 {
		t.Fatalf("saved %d times", len(f.saved))
	}
	got := f.saved[0]
	want := appcore.Config{ServerURL: "https://vpn2.example.org/", Provider: "github", GitHubClientID: "Iv1.new",
		OIDCIssuer: "https://login.example.org", OIDCClientID: "claimward", SocketPath: goodConfig.SocketPath}
	if got != want {
		t.Errorf("saved %+v\nwant  %+v", got, want)
	}
	if vm.Page.Get() != PageStatus || vm.Message.Get() != "" || vm.Detail.Get() != "https://vpn2.example.org/" {
		t.Errorf("after saving: page %d message %q detail %q", vm.Page.Get(), vm.Message.Get(), vm.Detail.Get())
	}

	// An incomplete configuration is saved, and said to be.
	vm.OpenSettings.Execute()
	vm.ProviderIndex.Set(1)
	vm.OIDCIssuer.Set("")
	vm.SaveSettings.Execute()
	if f.saved[1].Provider != "oidc" || !strings.HasPrefix(vm.Message.Get(), "Saved, but not complete: missing config: oidc_issuer") {
		t.Errorf("incomplete: %+v %q", f.saved[1], vm.Message.Get())
	}

	// What is not a URL is not saved.
	vm.OpenSettings.Execute()
	for _, bad := range []string{"vpn.example.org", "ftp://vpn.example.org", "https://"} {
		vm.ServerURL.Set(bad)
		vm.SaveSettings.Execute()
		if len(f.saved) != 2 || vm.Page.Get() != PageSettings || !strings.Contains(vm.Message.Get(), "http(s) URL") {
			t.Errorf("%q: saved %d page %d message %q", bad, len(f.saved), vm.Page.Get(), vm.Message.Get())
		}
	}
	// A save that fails stays on the form.
	vm.ServerURL.Set("https://vpn.example.org")
	f.updateErr = errors.New("access denied")
	vm.SaveSettings.Execute()
	if vm.Page.Get() != PageSettings || vm.Message.Get() != "access denied" {
		t.Errorf("failed save: page %d message %q", vm.Page.Get(), vm.Message.Get())
	}
	// An out-of-range provider is GitHub, the default.
	f.updateErr = nil
	vm.ProviderIndex.Set(7)
	vm.SaveSettings.Execute()
	if f.saved[len(f.saved)-1].Provider != "github" {
		t.Errorf("provider %q", f.saved[len(f.saved)-1].Provider)
	}
	vm.OpenSettings.Execute()
	vm.CloseSettings.Execute()
	if vm.Page.Get() != PageStatus {
		t.Error("Cancel does not leave the settings")
	}
	if vm.ConfigPath.Get() == "" {
		t.Error("the form does not say where it saves")
	}
}

// The log is replaced when it changes, and left alone (no flicker, no
// scroll reset) when it does not.
func TestTheLogFollowsTheCore(t *testing.T) {
	f := signedIn()
	f.st.Log = []string{"10:00:00  signed in"}
	vm := newVM(t, f)
	events := 0
	vm.Log.SubscribeChanged(func() { events++ })
	vm.Refresh()
	if events != 0 {
		t.Errorf("an unchanged log was rewritten (%d events)", events)
	}
	f.set(func(st *appcore.Status) { st.Log = append(st.Log, "10:00:05  connected") })
	vm.Refresh()
	if !reflect.DeepEqual(vm.Log.Slice(), []string{"10:00:00  signed in", "10:00:05  connected"}) {
		t.Errorf("log %v", vm.Log.Slice())
	}
	// A capped log that rotated (same length, new last line) is replaced.
	f.set(func(st *appcore.Status) { st.Log = []string{"10:00:05  connected", "10:00:09  disconnecting"} })
	vm.Refresh()
	if vm.Log.At(1) != "10:00:09  disconnecting" {
		t.Errorf("rotated log %v", vm.Log.Slice())
	}
}

// waitFor drains q until cond holds.
func waitFor(t *testing.T, q *Queue, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		q.Drain()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestPolling(t *testing.T) {
	f := signedOut()
	q := &Queue{}
	vm := NewViewModel(f, WithDispatch(func(fn func()) { go fn() }, q.Post))
	ctx, cancel := context.WithCancel(context.Background())
	vm.StartPolling(ctx, 5*time.Millisecond)
	waitFor(t, q, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.statuses >= 3 })
	if vm.State.Get() != "Signed out" {
		t.Errorf("state %q", vm.State.Get())
	}
	f.set(func(st *appcore.Status) { st.LoggedIn = true })
	waitFor(t, q, func() bool { return vm.State.Get() == "Disconnected" })
	cancel()
	time.Sleep(20 * time.Millisecond)
	q.Drain()
	f.mu.Lock()
	n := f.statuses
	f.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	q.Drain()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statuses > n+1 {
		t.Errorf("polling went on after its context ended: %d then %d", n, f.statuses)
	}
}

// The default opener refuses rather than doing something unseen.
func TestTheDefaultOpenerRefuses(t *testing.T) {
	f := signedOut()
	f.st.DeviceVerificationURI = "https://github.com/login/device"
	vm := newVM(t, f)
	vm.OpenSignInPage.Execute()
	if !strings.Contains(vm.Message.Get(), "no browser") {
		t.Errorf("message %q", vm.Message.Get())
	}
}

// The same number of tenants, different ones: the rows are rebuilt.
func TestTenantRowsFollowTheOffer(t *testing.T) {
	f := signedIn()
	f.st.Tenants = []hproto.Tenant{{ID: "a"}, {ID: "b"}}
	vm := newVM(t, f)
	f.set(func(st *appcore.Status) { st.Tenants = []hproto.Tenant{{ID: "a"}, {ID: "c"}} })
	vm.Refresh()
	if got := vm.TenantNames.Slice(); !reflect.DeepEqual(got, []string{chooseTenant, "a", "c"}) {
		t.Errorf("rows %v", got)
	}
}
