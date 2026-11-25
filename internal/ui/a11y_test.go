// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

// The painted Settings button is described to the platform's accessibility
// layer as a button with its label, at its own bounds.
func TestPaintedSettingsButtonIsInTheAccessibilityTree(t *testing.T) {
	v, _ := newTestView(t, signedOut())

	var found bool
	for _, e := range v.A11yElements() {
		if e.Name != "Settings" {
			continue
		}
		found = true
		if e.Role != string(toolkit.RoleButton) {
			t.Errorf("Settings has role %q, want %q", e.Role, toolkit.RoleButton)
		}
		b := v.w.settings.Bounds()
		if e.X != b.X || e.Y != b.Y || e.W != b.W || e.H != b.H {
			t.Errorf("Settings at %d,%d %dx%d, want its bounds %v", e.X, e.Y, e.W, e.H, b)
		}
	}
	if !found {
		t.Fatal("the painted Settings button is not described to the accessibility layer")
	}
}

func TestAccessibilityTreeBeforeTheFirstLayoutIsEmpty(t *testing.T) {
	v := NewView(NewViewModel(signedOut()), &Queue{})
	t.Cleanup(v.Close)
	if es := v.A11yElements(); es != nil {
		t.Fatalf("elements before any layout: %v", es)
	}
}
