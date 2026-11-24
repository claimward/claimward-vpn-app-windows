// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"testing"

	"github.com/go-widgets/toolkit"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
)

// settingsControls opens the settings page and returns what the host embeds.
func settingsControls(t *testing.T, provider string) (*View, *ViewModel, []toolkit.NativeControl) {
	t.Helper()
	f := signedOut()
	f.cfg = appcore.Config{Provider: provider}
	v, vm := newTestView(t, f)
	click(t, v, v.w.settings)
	frame(t, v)
	return v, vm, v.NativeControls()
}

func keys(cs []toolkit.NativeControl) []string {
	var out []string
	for _, c := range cs {
		if c.Visible {
			out = append(out, c.Key)
		}
	}
	return out
}

// Every text input on the page being shown is one native entry. The inputs of
// the provider panel that is not shown are still described, marked not
// Visible, so the host keeps its control hidden and finds it again by Key.
func TestNativeControlsAreOneEntryPerShownTextInput(t *testing.T) {
	_, _, cs := settingsControls(t, "github")
	if got := keys(cs); len(got) != 2 || got[0] != "server_url" || got[1] != "github_client_id" {
		t.Fatalf("github provider: native controls %v", got)
	}
	if len(cs) != 4 {
		t.Fatalf("%d native controls described, want one per text input (4)", len(cs))
	}
	for _, c := range cs {
		if c.Kind != toolkit.NativeEntry {
			t.Fatalf("%q: kind %v", c.Key, c.Kind)
		}
		if c.Visible && (c.Rect.W <= 0 || c.Rect.H <= 0) {
			t.Fatalf("%q: visible but unplaced %+v", c.Key, c.Rect)
		}
	}
	_, _, cs = settingsControls(t, "oidc")
	if got := keys(cs); len(got) != 3 || got[0] != "server_url" || got[1] != "oidc_issuer" || got[2] != "oidc_client_id" {
		t.Fatalf("oidc provider: native controls %v", got)
	}
}

func TestNativeTextReachesTheViewModelAndBack(t *testing.T) {
	v, vm, cs := settingsControls(t, "github")
	cs[0].OnText("https://other.example.org")
	if got := vm.ServerURL.Get(); got != "https://other.example.org" {
		t.Fatalf("view model server %q", got)
	}
	if got := v.w.server.Text().Get(); got != "https://other.example.org" {
		t.Fatalf("fallback entry %q", got)
	}
	vm.ServerURL.Set("https://third.example.org")
	if got := v.NativeControls()[0].Text; got != "https://third.example.org" {
		t.Fatalf("native control %q", got)
	}
}

func TestClaimingStandsTheFallbackDown(t *testing.T) {
	v, _, cs := settingsControls(t, "github")
	frame(t, v)
	cs[0].OnClaim(true)
	if !v.w.nServer.Claimed().Get() {
		t.Fatal("OnClaim did not claim the region")
	}
	if !frame(t, v) {
		t.Fatal("claiming did not ask for a repaint")
	}
}

func TestNativeControlsBeforeTheFirstLayoutIsEmpty(t *testing.T) {
	v := NewView(NewViewModel(signedOut()), &Queue{})
	t.Cleanup(v.Close)
	if cs := v.NativeControls(); cs != nil {
		t.Fatalf("controls before any layout: %v", cs)
	}
}
