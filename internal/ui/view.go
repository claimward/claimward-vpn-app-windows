// SPDX-License-Identifier: BSD-3-Clause
//
// The View. It builds the widgets from go-widgets/toolkit and binds every
// one of them to the ViewModel (viewmodel.go) through go-widgets/mvvmtk and
// go-widgets/mvvm. It NEVER writes a widget's state: the binders own that,
// so the file passes go-widgets/mvvmlint. What it does write is layout --
// boxes and their sizes -- which is the View's own business.
//
// The View is also the application.Handler the window drives (host.go):
// the framebuffer, the input, and the UI thread's clock.

package ui

import (
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/mvvmtk"
	"github.com/go-widgets/toolkit"
)

// Layout, in logical pixels (toolkit.Scaled turns them into device ones).
const (
	margin = 14
	gap    = 8
	rowH   = 26
	btnH   = 30
	titleH = 30
)

// widgets are the View's controls, built once and bound once.
type widgets struct {
	title    *toolkit.Label
	settings *toolkit.Button

	state, detail, account, tenant, helper *toolkit.Label
	busy, message                          *toolkit.Label

	deviceHint, deviceCode, deviceURI *toolkit.Label
	openPage, cancelSignIn            *toolkit.Button

	tenantLabel *toolkit.Label
	tenantPick  *toolkit.DropDown

	signIn, signOut, connect, disconnect, chooseTenant *toolkit.Button

	logTitle *toolkit.Label
	log      *toolkit.ListBox

	serverLabel, providerLabel, githubLabel, issuerLabel, clientLabel, pathLabel *toolkit.Label
	server, githubID, issuer, clientID                                           *toolkit.Entry
	provider                                                                     *toolkit.DropDown
	save, cancel                                                                 *toolkit.Button

	blankSignIn, blankTenant *toolkit.Label
}

// cards are the CardLayouts that show one of several things: the page, and
// the panels that appear only when they apply. Each one's Active is bound to
// a ViewModel observable.
type cards struct {
	page, signIn, tenant, provider *toolkit.CardLayout
}

// bindView builds the widgets and binds them; invalidate asks for a repaint
// and relayout asks for the cards to be laid out again.
func bindView(vm *ViewModel, invalidate, relayout func()) (*widgets, *cards, []func()) {
	w := &widgets{
		title:   toolkit.NewLabel("Claimward VPN"),
		state:   toolkit.NewLabel(""),
		detail:  toolkit.NewLabel(""),
		account: toolkit.NewLabel(""),
		tenant:  toolkit.NewLabel(""),
		helper:  toolkit.NewLabel(""),
		busy:    toolkit.NewLabel(""),
		message: toolkit.NewLabel(""),

		deviceHint:   toolkit.NewLabel("Open the sign-in page, then enter this code:"),
		deviceCode:   toolkit.NewLabel(""),
		deviceURI:    toolkit.NewLabel(""),
		openPage:     toolkit.NewButton("Open sign-in page", nil),
		cancelSignIn: toolkit.NewButton("Cancel sign-in", nil),

		tenantLabel: toolkit.NewLabel("Tenant for this session:"),
		tenantPick:  toolkit.NewDropDown(nil, 0),

		signIn:       toolkit.NewButton("Sign in", nil),
		signOut:      toolkit.NewButton("Sign out", nil),
		connect:      toolkit.NewButton("Connect", nil),
		disconnect:   toolkit.NewButton("Disconnect", nil),
		chooseTenant: toolkit.NewButton("Choose tenant", nil),

		logTitle: toolkit.NewLabel("Connection log"),
		log:      toolkit.NewListBox(nil),

		serverLabel:   toolkit.NewLabel("Server URL"),
		providerLabel: toolkit.NewLabel("Identity provider"),
		githubLabel:   toolkit.NewLabel("GitHub OAuth app client ID"),
		issuerLabel:   toolkit.NewLabel("OIDC issuer"),
		clientLabel:   toolkit.NewLabel("OIDC client ID"),
		pathLabel:     toolkit.NewLabel(""),
		server:        toolkit.NewEntry(""),
		githubID:      toolkit.NewEntry(""),
		issuer:        toolkit.NewEntry(""),
		clientID:      toolkit.NewEntry(""),
		provider:      toolkit.NewDropDown(providerLabels(), 0),
		save:          toolkit.NewButton("Save", nil),
		cancel:        toolkit.NewButton("Cancel", nil),

		blankSignIn: toolkit.NewLabel(""),
		blankTenant: toolkit.NewLabel(""),
	}
	w.settings = toolkit.NewButton("Settings", nil)
	w.server.Placeholder = "https://vpn.example.org"
	w.githubID.Placeholder = "Iv1.0123456789abcdef"
	w.issuer.Placeholder = "https://login.example.org"

	c := &cards{
		page: &toolkit.CardLayout{}, signIn: &toolkit.CardLayout{},
		tenant: &toolkit.CardLayout{}, provider: &toolkit.CardLayout{},
	}
	onCard := func() { relayout(); invalidate() }
	unbind := []func(){
		mvvmtk.BindLabel(w.state, vm.State, invalidate),
		mvvmtk.BindLabel(w.detail, vm.Detail, invalidate),
		mvvmtk.BindLabel(w.account, vm.Account, invalidate),
		mvvmtk.BindLabel(w.tenant, vm.Tenant, invalidate),
		mvvmtk.BindLabel(w.helper, vm.Helper, invalidate),
		mvvmtk.BindLabel(w.busy, vm.Busy, invalidate),
		mvvmtk.BindLabel(w.message, vm.Message, invalidate),
		mvvmtk.BindLabel(w.deviceCode, vm.DeviceCode, invalidate),
		mvvmtk.BindLabel(w.deviceURI, vm.DeviceURI, invalidate),
		mvvmtk.BindLabel(w.pathLabel, vm.ConfigPath, invalidate),

		bindCommand(w.settings, vm.OpenSettings, invalidate),
		bindCommand(w.openPage, vm.OpenSignInPage, invalidate),
		bindCommand(w.cancelSignIn, vm.CancelSignIn, invalidate),
		bindCommand(w.signIn, vm.SignIn, invalidate),
		bindCommand(w.signOut, vm.SignOut, invalidate),
		bindCommand(w.connect, vm.Connect, invalidate),
		bindCommand(w.disconnect, vm.Disconnect, invalidate),
		bindCommand(w.chooseTenant, vm.ChooseTenant, invalidate),
		bindCommand(w.save, vm.SaveSettings, invalidate),
		bindCommand(w.cancel, vm.CloseSettings, invalidate),

		mvvmtk.BindDropDownOptions(w.tenantPick, vm.TenantNames, func(s string) string { return s }, invalidate),
		mvvmtk.BindSelectedIndex(w.tenantPick, vm.TenantIndex, invalidate),
		mvvmtk.BindSelectedIndex(w.provider, vm.ProviderIndex, invalidate),
		mvvmtk.BindListItems(w.log, vm.Log, func(s string) string { return s }, invalidate),

		mvvmtk.BindEntryText(w.server, vm.ServerURL, invalidate),
		mvvmtk.BindEntryText(w.githubID, vm.GitHubClientID, invalidate),
		mvvmtk.BindEntryText(w.issuer, vm.OIDCIssuer, invalidate),
		mvvmtk.BindEntryText(w.clientID, vm.OIDCClientID, invalidate),

		mvvm.OneWay(vm.Page, &c.page.Active, onCard),
		mvvm.OneWay(vm.SignInPanel, &c.signIn.Active, onCard),
		mvvm.OneWay(vm.TenantPanel, &c.tenant.Active, onCard),
		mvvm.OneWay(vm.ProviderPanel, &c.provider.Active, onCard),
	}
	return w, c, unbind
}

// bindCommand is mvvmtk.BindCommand plus the button's Disabled state.
//
// mvvmtk.BindCommand (v0.14.0) shows a command that cannot run only by
// switching the button to the secondary style: the button still looks
// pressable, takes focus and answers a click (which Execute then ignores).
// A Connect button that looks live while a connection is under way invites
// the second click this app must not take, so the button is also disabled,
// from the same CanExecute.
func bindCommand(b *toolkit.Button, c *mvvm.Command, invalidate func()) (unbind func()) {
	u := mvvmtk.BindCommand(b, c, invalidate)
	set := func() { b.Disabled().Set(!c.CanExecute()) }
	set()
	unsub := c.SubscribeCanExecuteChanged(set)
	return func() { unsub(); u() }
}

func providerLabels() []string {
	out := make([]string, len(Providers))
	for i, p := range Providers {
		out[i] = p.Label
	}
	return out
}

// layout composes the widgets into boxes sized for the current metric
// scale. It is rebuilt when the scale changes (a window dragged to a
// display of another density), never per frame; the widgets and their
// bindings are untouched by it.
func layout(w *widgets, c *cards) toolkit.Widget {
	px := toolkit.Scaled
	row := func(ws ...toolkit.Widget) *toolkit.HBox {
		h := toolkit.NewHBox()
		h.Spacing = px(gap)
		for _, x := range ws {
			h.AddFlex(x, 1)
		}
		return h
	}
	card := func(l *toolkit.CardLayout, pages ...toolkit.Widget) *toolkit.Container {
		ct := toolkit.NewContainer(l)
		for _, p := range pages {
			ct.AddWidget(p)
		}
		return ct
	}

	device := toolkit.NewVBox()
	device.Spacing = px(4)
	device.AddFixed(w.deviceHint, px(rowH))
	device.AddFixed(w.deviceCode, px(rowH))
	device.AddFixed(w.deviceURI, px(rowH))
	device.AddFixed(row(w.openPage, w.cancelSignIn), px(btnH))

	tenantRow := toolkit.NewHBox()
	tenantRow.Spacing = px(gap)
	tenantRow.AddFixed(w.tenantLabel, px(170))
	tenantRow.AddFlex(w.tenantPick, 1)

	status := toolkit.NewVBox()
	status.Spacing = px(4)
	status.AddFixed(w.state, px(rowH))
	status.AddFixed(w.detail, px(rowH))
	status.AddFixed(w.account, px(rowH))
	status.AddFixed(w.tenant, px(rowH))
	status.AddFixed(w.helper, px(rowH))
	status.AddFixed(card(c.signIn, w.blankSignIn, device), 3*px(rowH)+px(btnH)+3*px(4))
	status.AddFixed(card(c.tenant, w.blankTenant, tenantRow), px(btnH))
	status.AddFixed(row(w.signIn, w.signOut), px(btnH))
	status.AddFixed(row(w.connect, w.disconnect, w.chooseTenant), px(btnH))
	status.AddFixed(w.logTitle, px(rowH))
	status.AddFlex(w.log, 1)

	github := toolkit.NewVBox()
	github.Spacing = px(4)
	github.AddFixed(w.githubLabel, px(rowH))
	github.AddFixed(w.githubID, px(rowH))
	oidc := toolkit.NewVBox()
	oidc.Spacing = px(4)
	oidc.AddFixed(w.issuerLabel, px(rowH))
	oidc.AddFixed(w.issuer, px(rowH))
	oidc.AddFixed(w.clientLabel, px(rowH))
	oidc.AddFixed(w.clientID, px(rowH))

	settings := toolkit.NewVBox()
	settings.Spacing = px(4)
	settings.AddFixed(w.serverLabel, px(rowH))
	settings.AddFixed(w.server, px(rowH))
	settings.AddFixed(w.providerLabel, px(rowH))
	settings.AddFixed(w.provider, px(rowH))
	settings.AddFixed(card(c.provider, github, oidc), 4*px(rowH)+3*px(4))
	settings.AddFixed(row(w.save, w.cancel), px(btnH))
	settings.AddFixed(w.pathLabel, px(rowH))
	settings.AddFlex(toolkit.NewLabel(""), 1)

	header := toolkit.NewHBox()
	header.Spacing = px(gap)
	header.AddFlex(w.title, 1)
	header.AddFixed(w.settings, px(110))

	root := toolkit.NewVBox()
	root.Spacing = px(gap)
	root.AddFixed(header, px(titleH))
	root.AddFlex(card(c.page, status, settings), 1)
	root.AddFixed(w.busy, px(rowH))
	root.AddFixed(w.message, px(rowH))
	return toolkit.NewPopoverHost(root)
}
