// SPDX-License-Identifier: BSD-3-Clause
//
// The ViewModel is an adapter over appvm (claimward-vpn-client pkg/appvm,
// tested there): these tests check what the adapter does -- every State is
// copied into the observables, every command reaches the model -- over a fake
// Core, with the results carried to the "UI thread" through a Queue.

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
	// loginWait, when set, makes Login wait for it or for its context;
	// connectWait does the same for Connect.
	loginWait, connectWait chan struct{}

	calls     []string
	setTenant []string
	saved     []appcore.Config
}

func (f *fakeCore) call(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeCore) Status() appcore.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
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
func (f *fakeCore) Connect(ctx context.Context) error {
	f.call("Connect")
	if f.connectWait != nil {
		select {
		case <-f.connectWait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

// newVM is a ViewModel over f whose results come back through q, refreshed
// once.
func newVM(t *testing.T, f *fakeCore, opts ...Option) (*ViewModel, *Queue) {
	t.Helper()
	q := &Queue{}
	vm := NewViewModel(f, append([]Option{WithPost(q.Post)}, opts...)...)
	t.Cleanup(func() { vm.Stop(); vm.Wait() })
	vm.Refresh()
	q.Drain()
	return vm, q
}

// settle waits for every action to finish and runs what they posted.
func settle(vm *ViewModel, q *Queue) {
	vm.Wait()
	q.Drain()
}

// run executes a command and settles.
func run(vm *ViewModel, q *Queue, execute func()) {
	execute()
	settle(vm, q)
}

func TestStatusLines(t *testing.T) {
	f := signedOut()
	vm := NewViewModel(f)
	if vm.State.Get() == "" || vm.TrayStatus.Get() == "" {
		t.Errorf("before the first status: %q %q", vm.State.Get(), vm.TrayStatus.Get())
	}
	vm.Refresh()
	if vm.State.Get() != "Signed out" || vm.Detail.Get() != goodConfig.ServerURL || vm.Account.Get() != "Not signed in" {
		t.Errorf("signed out: %q %q %q", vm.State.Get(), vm.Detail.Get(), vm.Account.Get())
	}
	if !strings.Contains(vm.Helper.Get(), "running") || vm.TrayStatus.Get() != "Claimward: signed out" {
		t.Errorf("helper %q tray %q", vm.Helper.Get(), vm.TrayStatus.Get())
	}

	f.set(func(st *appcore.Status) { st.LoggedIn, st.Email = true, "ada@example.org" })
	vm.Refresh()
	if vm.State.Get() != "Disconnected" || vm.Account.Get() != "Signed in as ada@example.org" || vm.Detail.Get() != goodConfig.ServerURL {
		t.Errorf("signed in: %q %q %q", vm.State.Get(), vm.Account.Get(), vm.Detail.Get())
	}

	f.set(func(st *appcore.Status) {
		st.Connected, st.AssignedIP, st.Interface = true, "10.80.0.7/32", "Claimward"
	})
	vm.Refresh()
	if vm.State.Get() != "Connected" || !strings.Contains(vm.Detail.Get(), "10.80.0.7/32") || !strings.Contains(vm.TrayStatus.Get(), "10.80.0.7/32") {
		t.Errorf("connected: %q %q %q", vm.State.Get(), vm.Detail.Get(), vm.TrayStatus.Get())
	}

	f.set(func(st *appcore.Status) { *st = appcore.Status{ConfigError: "no server URL", HelperInstalled: false} })
	vm.Refresh()
	if vm.State.Get() != "Not configured" || vm.Detail.Get() != "no server URL" || !strings.Contains(vm.Helper.Get(), "not running") {
		t.Errorf("unconfigured: %q %q %q", vm.State.Get(), vm.Detail.Get(), vm.Helper.Get())
	}
}

func TestTheDefaultPostRunsInline(t *testing.T) {
	vm := NewViewModel(signedIn())
	vm.Apply(signedIn().st)
	if vm.State.Get() != "Disconnected" {
		t.Errorf("state %q", vm.State.Get())
	}
}

func TestCommandsFollowTheModel(t *testing.T) {
	f := signedOut()
	vm, q := newVM(t, f)
	can := func() [5]bool {
		return [5]bool{vm.SignIn.CanExecute(), vm.Connect.CanExecute(), vm.Disconnect.CanExecute(), vm.SignOut.CanExecute(), vm.ChooseTenant.CanExecute()}
	}
	if got := can(); got != [5]bool{true, false, false, false, false} {
		t.Errorf("signed out: %v", got)
	}
	f.set(func(st *appcore.Status) { st.LoggedIn = true })
	vm.Refresh()
	q.Drain()
	if got := can(); got != [5]bool{false, true, false, true, true} {
		t.Errorf("signed in: %v", got)
	}
	f.set(func(st *appcore.Status) { st.Connected = true })
	vm.Refresh()
	q.Drain()
	if got := can(); got != [5]bool{false, false, true, true, true} {
		t.Errorf("connected: %v", got)
	}
}

func TestConnectAndDisconnectRunAwayFromTheUIThread(t *testing.T) {
	f := signedIn()
	f.connectWait = make(chan struct{})
	vm, q := newVM(t, f)
	vm.Connect.Execute()
	// The call is under way: Busy is posted, nothing else is offered.
	deadline := time.Now().Add(5 * time.Second)
	for f.called("Connect") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	q.Drain()
	if vm.Busy.Get() == "" || vm.Connect.CanExecute() || vm.Disconnect.CanExecute() {
		t.Errorf("while connecting: busy %q connect %v disconnect %v", vm.Busy.Get(), vm.Connect.CanExecute(), vm.Disconnect.CanExecute())
	}
	close(f.connectWait)
	settle(vm, q)
	if vm.State.Get() != "Connected" || vm.Busy.Get() != "" || !vm.Disconnect.CanExecute() {
		t.Errorf("connected: %q busy %q", vm.State.Get(), vm.Busy.Get())
	}
	run(vm, q, vm.Disconnect.Execute)
	if f.called("Disconnect") != 1 || vm.State.Get() != "Disconnected" {
		t.Errorf("disconnect %d state %q", f.called("Disconnect"), vm.State.Get())
	}
	run(vm, q, vm.SignOut.Execute)
	if f.called("Logout") != 1 || vm.State.Get() != "Signed out" {
		t.Errorf("logout %d state %q", f.called("Logout"), vm.State.Get())
	}
}

func TestAFailureIsTheMessage(t *testing.T) {
	f := signedIn()
	f.connectErr = errors.New("helper refused")
	vm, q := newVM(t, f)
	run(vm, q, vm.Connect.Execute)
	if !strings.Contains(vm.Message.Get(), "helper refused") {
		t.Errorf("message %q", vm.Message.Get())
	}
}

func TestSigningIn(t *testing.T) {
	f := signedOut()
	f.loginWait = make(chan struct{})
	var opened []string
	vm, q := newVM(t, f, WithOpener(func(u string) error { opened = append(opened, u); return nil }))
	vm.SignIn.Execute()
	deadline := time.Now().Add(5 * time.Second)
	for f.called("Login") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	f.set(func(st *appcore.Status) {
		st.DeviceVerificationURI, st.DeviceUserCode = "https://github.com/login/device", "WDJB-MJHT"
	})
	vm.Refresh()
	q.Drain()
	if vm.SignInPanel.Get() != PanelShown || vm.DeviceURI.Get() != "https://github.com/login/device" || vm.DeviceCode.Get() != "WDJB-MJHT" {
		t.Fatalf("panel %d %q %q", vm.SignInPanel.Get(), vm.DeviceURI.Get(), vm.DeviceCode.Get())
	}
	if !vm.OpenSignInPage.CanExecute() || !vm.CancelSignIn.CanExecute() {
		t.Fatal("the prompt does not offer its two buttons")
	}
	vm.OpenSignInPage.Execute()
	settleOpen := time.Now().Add(5 * time.Second)
	for len(opened) == 0 && time.Now().Before(settleOpen) {
		time.Sleep(time.Millisecond)
	}
	if len(opened) != 1 || opened[0] != "https://github.com/login/device" {
		t.Errorf("opened %v", opened)
	}
	close(f.loginWait)
	settle(vm, q)
	if vm.State.Get() != "Disconnected" || vm.SignInPanel.Get() != PanelHidden || vm.DeviceCode.Get() != "" {
		t.Errorf("after the sign-in: %q panel %d code %q", vm.State.Get(), vm.SignInPanel.Get(), vm.DeviceCode.Get())
	}
}

func TestCancellingASignIn(t *testing.T) {
	f := signedOut()
	f.loginWait = make(chan struct{})
	vm, q := newVM(t, f)
	vm.SignIn.Execute()
	deadline := time.Now().Add(5 * time.Second)
	for f.called("Login") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	q.Drain()
	if !vm.CancelSignIn.CanExecute() {
		t.Fatal("a sign-in under way cannot be cancelled")
	}
	run(vm, q, vm.CancelSignIn.Execute)
	if vm.Message.Get() != "Sign-in cancelled." || vm.State.Get() != "Signed out" {
		t.Errorf("message %q state %q", vm.Message.Get(), vm.State.Get())
	}
}

func TestTheTenantChoice(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}, {ID: "hpc", Name: "HPC"}}
	vm, q := newVM(t, f)
	if vm.TenantPanel.Get() != PanelHidden {
		t.Error("the tenant choice shows before the server offered one")
	}
	run(vm, q, vm.Connect.Execute)
	if vm.TenantPanel.Get() != PanelShown || vm.State.Get() == "Connected" {
		t.Fatalf("after ErrTenantRequired: panel %d state %q message %q", vm.TenantPanel.Get(), vm.State.Get(), vm.Message.Get())
	}
	want := []string{"Choose a tenant…", "Chemistry (chem)", "HPC (hpc)"}
	if got := vm.TenantNames.Slice(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if !strings.Contains(vm.Message.Get(), "several tenants") {
		t.Errorf("message %q", vm.Message.Get())
	}

	// A refresh that only repeats what the model says is not the person
	// choosing.
	vm.Refresh()
	q.Drain()
	if len(f.setTenant) != 0 {
		t.Fatalf("a poll chose a tenant: %v", f.setTenant)
	}
	// The person picks the third row.
	vm.TenantIndex.Set(2)
	q.Drain()
	if len(f.setTenant) != 1 || f.setTenant[0] != "hpc" || vm.TenantIndex.Get() != 2 || vm.Tenant.Get() != "Tenant: HPC (hpc)" {
		t.Errorf("SetTenant %v index %d tenant %q", f.setTenant, vm.TenantIndex.Get(), vm.Tenant.Get())
	}
	run(vm, q, vm.Connect.Execute)
	if vm.State.Get() != "Connected" || vm.TenantPanel.Get() != PanelHidden {
		t.Errorf("state %q panel %d", vm.State.Get(), vm.TenantPanel.Get())
	}
}

func TestChoosingATenantAsksTheServer(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem"}, {ID: "hpc"}}
	vm, q := newVM(t, f)
	run(vm, q, vm.ChooseTenant.Execute)
	if f.called("Tenants") != 1 || vm.TenantPanel.Get() != PanelShown || len(vm.TenantNames.Slice()) != 3 {
		t.Errorf("Tenants %d panel %d rows %v", f.called("Tenants"), vm.TenantPanel.Get(), vm.TenantNames.Slice())
	}
}

func TestSettings(t *testing.T) {
	f := signedOut()
	f.cfg = appcore.Config{ServerURL: "https://old.example.org", Provider: "oidc", OIDCIssuer: "https://login.example.org", OIDCClientID: "cid"}
	vm, q := newVM(t, f, WithConfigPath("Saved to /here"))
	if vm.ConfigPath.Get() != "Saved to /here" {
		t.Errorf("path %q", vm.ConfigPath.Get())
	}
	run(vm, q, vm.OpenSettings.Execute)
	if vm.Page.Get() != PageSettings || vm.ServerURL.Get() != "https://old.example.org" || vm.ProviderIndex.Get() != 1 ||
		vm.OIDCIssuer.Get() != "https://login.example.org" || vm.OIDCClientID.Get() != "cid" || vm.ProviderPanel.Get() != ProviderOIDC {
		t.Fatalf("form: page %d %q %d %q %q panel %d", vm.Page.Get(), vm.ServerURL.Get(), vm.ProviderIndex.Get(), vm.OIDCIssuer.Get(), vm.OIDCClientID.Get(), vm.ProviderPanel.Get())
	}
	// Opening them again, from the settings page, changes nothing.
	vm.ServerURL.Set("https://typed.example.org")
	vm.openSettings()
	if vm.ServerURL.Get() != "https://typed.example.org" {
		t.Error("a refused open overwrote what was typed")
	}
	vm.ServerURL.Set("https://old.example.org")
	if vm.Page.Get() != PageSettings || vm.OpenSettings.CanExecute() {
		t.Error("the settings page offers to open itself")
	}

	vm.ProviderIndex.Set(0)
	if vm.ProviderPanel.Get() != ProviderGitHub {
		t.Error("GitHub shows the OIDC fields")
	}
	vm.ProviderIndex.Set(2)
	if vm.ProviderPanel.Get() != ProviderOIDC {
		t.Error("go-authn shows the GitHub field")
	}
	vm.ProviderIndex.Set(-1)
	if vm.ProviderPanel.Get() != ProviderGitHub {
		t.Error("an unknown provider shows the OIDC fields")
	}

	// A bad URL is refused, in the form.
	vm.ServerURL.Set("ftp://nope")
	run(vm, q, vm.SaveSettings.Execute)
	if f.called("UpdateConfig") != 0 || !strings.Contains(vm.Message.Get(), "http(s) URL") || vm.Page.Get() != PageSettings {
		t.Errorf("a bad URL: saved %d message %q page %d", f.called("UpdateConfig"), vm.Message.Get(), vm.Page.Get())
	}

	// An incomplete configuration is saved, and said to be.
	vm.ServerURL.Set("  https://new.example.org ")
	vm.ProviderIndex.Set(0)
	vm.GitHubClientID.Set("")
	run(vm, q, vm.SaveSettings.Execute)
	if len(f.saved) != 1 || f.saved[0].ServerURL != "https://new.example.org" || f.saved[0].Provider != "github" {
		t.Fatalf("saved %+v", f.saved)
	}
	if vm.Page.Get() != PageStatus || !strings.HasPrefix(vm.Message.Get(), "Saved, but not complete") {
		t.Errorf("page %d message %q", vm.Page.Get(), vm.Message.Get())
	}

	// An out-of-range provider is GitHub; a failure keeps the form open.
	run(vm, q, vm.OpenSettings.Execute)
	vm.ProviderIndex.Set(7)
	vm.GitHubClientID.Set("Iv1.abc")
	f.updateErr = errors.New("disk full")
	run(vm, q, vm.SaveSettings.Execute)
	if vm.Page.Get() != PageSettings || !strings.Contains(vm.Message.Get(), "disk full") {
		t.Errorf("a failed save: page %d message %q", vm.Page.Get(), vm.Message.Get())
	}
	vm.CloseSettings.Execute()
	q.Drain()
	if vm.Page.Get() != PageStatus || vm.Message.Get() != "" {
		t.Errorf("after cancel: page %d message %q", vm.Page.Get(), vm.Message.Get())
	}
}

func TestTheLogGrowsAndIsReplaced(t *testing.T) {
	f := signedIn()
	f.st.Log = []string{"a"}
	vm, q := newVM(t, f)
	f.set(func(st *appcore.Status) { st.Log = []string{"a", "b"} })
	vm.Refresh()
	q.Drain()
	if got := vm.Log.Slice(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("appended: %v", got)
	}
	vm.Refresh() // unchanged
	q.Drain()
	if got := vm.Log.Slice(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("unchanged: %v", got)
	}
	f.set(func(st *appcore.Status) { st.Log = []string{"x"} })
	vm.Refresh()
	q.Drain()
	if got := vm.Log.Slice(); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("replaced: %v", got)
	}
}

func TestPollingStartsAndStops(t *testing.T) {
	f := signedIn()
	q := &Queue{}
	vm := NewViewModel(f, WithPost(q.Post), WithInterval(5*time.Millisecond, 5*time.Millisecond))
	vm.Start()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		q.Drain()
		if vm.State.Get() == "Disconnected" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if vm.State.Get() != "Disconnected" {
		t.Fatalf("the poll did not deliver a status: %q", vm.State.Get())
	}
	f.set(func(st *appcore.Status) { st.Connected = true })
	for time.Now().Before(deadline) {
		q.Drain()
		if vm.State.Get() == "Connected" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if vm.State.Get() != "Connected" {
		t.Fatalf("a later poll was not delivered: %q", vm.State.Get())
	}
	vm.Stop()
	vm.Wait()
}
