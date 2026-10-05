// SPDX-License-Identifier: BSD-3-Clause
//
// View tests: the real widget tree, bound to a ViewModel over a fake Core,
// driven through the application.Handler methods the window calls -- input
// in, pixels out -- with no window.

package ui

import (
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/application"
	"github.com/go-widgets/toolkit"
)

const testW, testH = 460, 720

// newTestView is a View over f, sized and drawn once.
func newTestView(t *testing.T, f *fakeCore) (*View, *ViewModel) {
	t.Helper()
	q := &Queue{}
	vm := NewViewModel(f, WithDispatch(func(fn func()) { fn() }, q.Post))
	v := NewView(vm, q)
	t.Cleanup(v.Close)
	vm.Refresh()
	v.Resize(testW, testH, 1)
	frame(t, v)
	return v, vm
}

// frame runs one tick and reports whether it drew.
func frame(t *testing.T, v *View) bool {
	t.Helper()
	buf, w, h, changed := v.Frame()
	if changed && (w != testW || h != testH || len(buf) != 4*w*h) {
		t.Fatalf("frame %dx%d with %d bytes", w, h, len(buf))
	}
	return changed
}

func click(t *testing.T, v *View, w toolkit.Widget) {
	t.Helper()
	r := w.Bounds()
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("%T is not on screen (%+v)", w, r)
	}
	// Widget bounds are in surface coordinates; so is the pointer.
	x, y := r.X+r.W/2, r.Y+r.H/2
	v.MouseDown(x, y)
	v.MouseUp(x, y)
	frame(t, v)
}

func visible(w toolkit.Widget) bool { r := w.Bounds(); return r.W > 0 && r.H > 0 }

func TestTheWindowDrawsTheStatus(t *testing.T) {
	v, _ := newTestView(t, signedIn())
	if v.w.state.Text().Get() != "Disconnected" || v.w.account.Text().Get() != "Signed in as ada@example.org" {
		t.Errorf("labels %q %q", v.w.state.Text().Get(), v.w.account.Text().Get())
	}
	if frame(t, v) {
		t.Error("an idle window redrew")
	}
	if v.NeedsPresent() {
		t.Error("an idle window asks to be presented")
	}
	// The background is painted.
	buf, _, _, _ := v.Frame()
	if bg := v.theme.Background; buf[0] != bg.R || buf[1] != bg.G || buf[2] != bg.B {
		t.Errorf("top-left pixel %v, want %v", buf[:4], bg)
	}
	for _, w := range []toolkit.Widget{v.w.connect, v.w.signOut, v.w.log, v.w.settings} {
		if !visible(w) {
			t.Errorf("%T is not laid out", w)
		}
	}
	for _, w := range []toolkit.Widget{v.w.server, v.w.deviceCode, v.w.tenantPick} {
		if visible(w) {
			t.Errorf("%T shows on the status page", w)
		}
	}
}

// A click on Connect runs the command, and the result reaches the labels
// through the queue, on the next frame.
func TestClickingConnect(t *testing.T) {
	f := signedIn()
	v, vm := newTestView(t, f)
	if v.w.connect.Disabled().Get() || !v.w.disconnect.Disabled().Get() {
		t.Error("Connect and Disconnect are not enabled as the state says")
	}
	click(t, v, v.w.connect)
	frame(t, v)
	if f.called("Connect") != 1 || vm.State.Get() != "Connected" || v.w.state.Text().Get() != "Connected" {
		t.Errorf("Connect called %d times; state %q", f.called("Connect"), v.w.state.Text().Get())
	}
	if !v.w.connect.Disabled().Get() || v.w.disconnect.Disabled().Get() {
		t.Error("after connecting, Connect is enabled or Disconnect is not")
	}
	// A disabled button does nothing.
	click(t, v, v.w.connect)
	if f.called("Connect") != 1 {
		t.Error("a disabled Connect connected")
	}
	click(t, v, v.w.disconnect)
	frame(t, v)
	if f.called("Disconnect") != 1 || v.w.state.Text().Get() != "Disconnected" {
		t.Errorf("Disconnect called %d times; state %q", f.called("Disconnect"), v.w.state.Text().Get())
	}
}

// The panels appear when the ViewModel says, laid out again.
func TestPanelsAppearWhenTheyApply(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}, {ID: "hpc", Name: "HPC"}}
	f.st.DeviceVerificationURI, f.st.DeviceUserCode = "https://github.com/login/device", "WDJB-MJHT"
	v, vm := newTestView(t, f)
	if !visible(v.w.deviceCode) || !visible(v.w.openPage) || v.w.deviceCode.Text().Get() != "WDJB-MJHT" {
		t.Errorf("the device prompt is not shown: %+v %q", v.w.deviceCode.Bounds(), v.w.deviceCode.Text().Get())
	}
	if visible(v.w.tenantPick) {
		t.Error("the tenant choice shows before a choice is needed")
	}
	click(t, v, v.w.connect)
	frame(t, v)
	if vm.TenantPanel.Get() != PanelShown || !visible(v.w.tenantPick) {
		t.Fatalf("the tenant choice is not shown after ErrTenantRequired: %+v", v.w.tenantPick.Bounds())
	}
	if v.w.message.Text().Get() != "You belong to several tenants: choose one, then Connect." {
		t.Errorf("message %q", v.w.message.Text().Get())
	}
	// Choose through the DropDown, as a person does: open it, click a row.
	click(t, v, v.w.tenantPick)
	if !v.w.tenantPick.Open().Get() {
		t.Fatal("the DropDown did not open")
	}
	pb := v.w.tenantPick.PopoverBounds()
	row := pb.H / 3
	v.MouseDown(pb.X+pb.W/2, pb.Y+2*row+row/2) // the third row: HPC
	v.MouseUp(pb.X+pb.W/2, pb.Y+2*row+row/2)
	frame(t, v)
	if len(f.setTenant) != 1 || f.setTenant[0] != "hpc" {
		t.Fatalf("SetTenant %v", f.setTenant)
	}
	if v.w.tenant.Text().Get() != "Tenant: HPC" {
		t.Errorf("tenant line %q", v.w.tenant.Text().Get())
	}
}

// The settings page: switched to, typed into, saved.
func TestTypingIntoTheSettings(t *testing.T) {
	f := signedOut()
	f.cfg = appcore.Config{Provider: "github"}
	v, vm := newTestView(t, f)
	click(t, v, v.w.settings)
	frame(t, v)
	if vm.Page.Get() != PageSettings || !visible(v.w.server) || visible(v.w.connect) {
		t.Fatalf("page %d; server field %+v", vm.Page.Get(), v.w.server.Bounds())
	}
	if !visible(v.w.githubID) || visible(v.w.issuer) {
		t.Error("the GitHub provider does not show the GitHub field alone")
	}
	click(t, v, v.w.server)
	for _, r := range "https://vpn.example.org" {
		v.Key("", r)
	}
	v.Key("Backspace", 0)
	v.Key("Left", 0)
	v.Key("Right", 0)
	v.Key("", 'g')
	frame(t, v)
	if vm.ServerURL.Get() != "https://vpn.example.org" {
		t.Errorf("typed %q", vm.ServerURL.Get())
	}
	// Ctrl+V pastes, into the field that has focus.
	toolkit.SetClipboardText("Iv1.pasted")
	click(t, v, v.w.githubID)
	v.Shortcut('v', true, false)
	v.Shortcut('v', false, false) // no modifier: not a shortcut
	frame(t, v)
	if vm.GitHubClientID.Get() != "Iv1.pasted" {
		t.Errorf("pasted %q", vm.GitHubClientID.Get())
	}
	click(t, v, v.w.save)
	frame(t, v)
	if len(f.saved) != 1 || f.saved[0].ServerURL != "https://vpn.example.org" || f.saved[0].GitHubClientID != "Iv1.pasted" {
		t.Fatalf("saved %+v", f.saved)
	}
	if vm.Page.Get() != PageStatus || !visible(v.w.connect) || visible(v.w.server) {
		t.Error("saving did not return to the status page")
	}
}

func TestTheProviderChoiceSwitchesTheFields(t *testing.T) {
	v, vm := newTestView(t, signedOut())
	vm.OpenSettings.Execute()
	frame(t, v)
	vm.ProviderIndex.Set(1)
	frame(t, v)
	if v.w.provider.Selected().Get() != 1 || !visible(v.w.issuer) || !visible(v.w.clientID) || visible(v.w.githubID) {
		t.Errorf("OIDC: issuer %+v github %+v", v.w.issuer.Bounds(), v.w.githubID.Bounds())
	}
}

func TestTheLogScrolls(t *testing.T) {
	f := signedIn()
	for i := 0; i < 200; i++ {
		f.st.Log = append(f.st.Log, "10:00:00  line")
	}
	v, _ := newTestView(t, f)
	if len(v.w.log.Items) != 200 {
		t.Fatalf("log rows %d", len(v.w.log.Items))
	}
	r := v.w.log.Bounds()
	v.MouseMove(r.X+10, r.Y+10)
	v.Scroll(120)
	v.Scroll(5)
	v.Scroll(-5)
	v.Scroll(0)
	if !frame(t, v) {
		t.Error("scrolling did not redraw")
	}
}

func TestAppearanceAndScale(t *testing.T) {
	v, _ := newTestView(t, signedOut())
	v.SystemAppearance(application.SystemAppearance{Dark: true})
	if !frame(t, v) || v.theme.Background != toolkit.DefaultDark().Background {
		t.Error("dark mode not applied")
	}
	v.SystemAppearance(application.SystemAppearance{})
	if !frame(t, v) || v.theme.Background != toolkit.DefaultLight().Background {
		t.Error("light mode not applied")
	}
	// A display of another density rebuilds the boxes at its scale.
	old := toolkit.MetricScale()
	t.Cleanup(func() { toolkit.SetMetricScale(old) })
	h := v.w.connect.Bounds().H
	toolkit.SetMetricScale(2)
	v.Resize(testW*2, testH*2, 2)
	v.Frame()
	if got := v.w.connect.Bounds().H; got != 2*h {
		t.Errorf("at scale 2 the button is %d high, want %d", got, 2*h)
	}
}

func TestAWindowNotYetSized(t *testing.T) {
	q := &Queue{}
	vm := NewViewModel(signedOut(), WithDispatch(func(fn func()) { fn() }, q.Post))
	v := NewView(vm, q)
	defer v.Close()
	if buf, _, _, changed := v.Frame(); buf != nil || changed {
		t.Error("an unsized window drew")
	}
	v.Resize(0, 10, 1) // ignored
	v.MouseDown(1, 1)  // no tree yet: ignored
	if buf, _, _, _ := v.Frame(); buf != nil {
		t.Error("a zero-size resize allocated")
	}
	if !v.NeedsPresent() || !v.PresentImmediate() || v.PresentThrottle() {
		t.Error("present gate")
	}
}

func TestQueueRunsWhatItsFunctionsPost(t *testing.T) {
	q := &Queue{}
	var got []int
	q.Post(func() { got = append(got, 1); q.Post(func() { got = append(got, 3) }) })
	q.Post(func() { got = append(got, 2) })
	if !q.Pending() {
		t.Error("nothing pending")
	}
	q.Drain()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 || q.Pending() {
		t.Errorf("ran %v", got)
	}
}

// A move with the button held is a drag (a scrollbar thumb), not a hover.
func TestADragIsADrag(t *testing.T) {
	v, _ := newTestView(t, signedIn())
	r := v.w.log.Bounds()
	v.MouseDown(r.X+r.W-3, r.Y+5)
	v.MouseMove(r.X+r.W-3, r.Y+40)
	v.MouseUp(r.X+r.W-3, r.Y+40)
	v.MouseMove(r.X+5, r.Y+5)
	if v.pressed {
		t.Error("still pressed after the release")
	}
}

func TestUseOpenTypeText(t *testing.T) {
	if err := UseOpenTypeText(); err != nil {
		t.Fatal(err)
	}
}
