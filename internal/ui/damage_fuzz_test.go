// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/toolkit"
)

// explain names where a presented frame is stale and the widgets there, so a
// failure says which one reported too little.
func explain(v *View, shown []byte) string {
	x0, y0, x1, y1 := 1<<30, 1<<30, -1, -1
	for i := range shown {
		if shown[i] != v.buf[i] {
			x, y := (i/4)%v.width, (i/4)/v.width
			x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
		}
	}
	i := (y0*v.width + x0) * 4
	out := fmt.Sprintf("stale x %d-%d y %d-%d of %dx%d, shown %v want %v; widgets there:", x0, x1, y0, y1, v.width, v.high, shown[i:i+4], v.buf[i:i+4])
	var walk func(w toolkit.Widget, depth int)
	walk = func(w toolkit.Widget, depth int) {
		// Widgets within a few pixels count: a control can draw just outside
		// itself, which is the case worth seeing.
		if b := w.Bounds(); b.Contains(x0-8, y0-8) || b.Contains(x1+8, y1+8) || b.Contains(x0, y0) || b.Contains(x1, y1) {
			out += fmt.Sprintf("\n  %*s%T %v", depth*2, "", w, b)
		}
		if cp, ok := w.(interface{ Children() []toolkit.Widget }); ok {
			for _, c := range cp.Children() {
				walk(c, depth+1)
			}
		}
	}
	walk(v.root, 0)
	return out
}

// An open list floats over the window outside its widget: it is damaged where
// it is drawn while it shows, and where it was when it goes.
func TestAnOpenPopoverIsDamagedWhereItIsDrawnAndWhereItWas(t *testing.T) {
	f := signedIn()
	f.offered = []hproto.Tenant{{ID: "chem", Name: "Chemistry"}, {ID: "hpc", Name: "HPC"}}
	v, vm := newTestView(t, f)
	click(t, v, v.w.connect)
	settleView(t, v, vm)
	var o host
	o.present(v)
	same := func(what string) {
		t.Helper()
		o.present(v)
		if !bytes.Equal(o.shown, v.buf) {
			t.Fatalf("%s: %s", what, explain(v, o.shown))
		}
	}
	click(t, v, v.w.tenantPick)
	same("the list opening")
	pb := v.w.tenantPick.PopoverBounds()
	v.MouseMove(pb.X+pb.W/2, pb.Y+pb.H/2)
	same("the pointer over the list")
	v.MouseDown(1, 1) // anywhere else closes it
	v.MouseUp(1, 1)
	same("the list closing")
}

// FuzzIncrementalPresent: after any sequence of view-model states, pointer
// moves, presses, keys, wheel turns, frames nobody presented, theme changes
// and resizes, what a damage-driven host shows equals the full frame, pixel
// for pixel. The seed picks the sequence.
func FuzzIncrementalPresent(f *testing.F) {
	for _, s := range []int64{1, 2, 3, 7, 42, 99, 1234} {
		f.Add(s, uint8(60))
	}
	f.Fuzz(func(t *testing.T, seed int64, steps uint8) {
		r := rand.New(rand.NewSource(seed))
		fk := signedOut()
		fk.st = randomStatus(r)
		v, vm := newTestView(t, fk)
		var o host
		o.present(v)
		for step := 0; step < int(steps%90); step++ {
			op := r.Intn(14)
			switch op {
			case 0: // a resize
				v.Resize(300+r.Intn(500), 400+r.Intn(500), 1)
			case 1, 2, 3: // the pointer
				v.MouseMove(r.Intn(v.width), r.Intn(v.high))
			case 4:
				v.MouseDown(r.Intn(v.width), r.Intn(v.high))
				v.MouseMove(r.Intn(v.width), r.Intn(v.high))
				v.MouseUp(r.Intn(v.width), r.Intn(v.high))
			case 5:
				v.Scroll(r.Intn(200) - 100)
			case 6:
				v.Key("Tab", 0)
			case 7: // frames the host skips
				v.Frame()
			default: // a new state
				show(fk, vm, randomStatus(r))
			}
			if r.Intn(3) != 0 { // the host does not ask after every frame
				o.present(v)
				if !bytes.Equal(o.shown, v.buf) {
					t.Fatalf("seed %d step %d op %d: the incrementally presented frame differs from a full repaint: %s", seed, step, op, explain(v, o.shown))
				}
			}
		}
		v.Frame()
		o.present(v)
		if !bytes.Equal(o.shown, v.buf) {
			t.Fatalf("seed %d: the last frame differs from a full repaint", seed)
		}
		vm.Wait()
	})
}
