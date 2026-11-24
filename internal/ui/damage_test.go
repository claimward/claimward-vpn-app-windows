// SPDX-License-Identifier: BSD-3-Clause

package ui

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
)

// host is a window that presents only what DamageRects says changed, the way
// application's surface does: a retained framebuffer, updated rectangle by
// rectangle, or whole when no rectangle is reported.
type host struct {
	shown []byte
	w, h  int
	rects int // rectangles presented, 0 for a whole-frame present
	bytes int // bytes presented
}

func (o *host) present(v *View) {
	pix, w, h, _ := v.Frame()
	damage := v.DamageRects()
	if len(o.shown) != len(pix) || o.w != w || o.h != h {
		o.shown, o.w, o.h = make([]byte, len(pix)), w, h
		damage = nil
	}
	if len(damage) == 0 {
		copy(o.shown, pix)
		o.bytes += len(pix)
		return
	}
	for _, r := range damage {
		for y := r.Y; y < r.Y+r.H; y++ {
			a := (y*w + r.X) * 4
			copy(o.shown[a:a+r.W*4], pix[a:a+r.W*4])
			o.bytes += r.W * 4
		}
		o.rects++
	}
}

func randomStatus(r *rand.Rand) appcore.Status {
	st := appcore.Status{
		ConfigOK: r.Intn(8) != 0, HelperInstalled: r.Intn(4) != 0,
		LoggedIn: r.Intn(3) != 0, ServerURL: "https://vpn.example.org",
	}
	if !st.ConfigOK {
		st.ConfigError = "missing config: server_url"
	}
	if st.LoggedIn {
		st.Email = fmt.Sprintf("user%d@example.org", r.Intn(100))
		st.Connected = r.Intn(2) == 0
		if st.Connected {
			st.AssignedIP, st.Interface = fmt.Sprintf("10.80.0.%d/32", r.Intn(250)), "Claimward"
		}
		for i := r.Intn(4); i > 0; i-- {
			st.Tenants = append(st.Tenants, hproto.Tenant{ID: fmt.Sprintf("t%d", i), Name: fmt.Sprintf("Tenant %d", i)})
		}
		st.TenantRequired = len(st.Tenants) > 1 && r.Intn(2) == 0
	} else if r.Intn(3) == 0 {
		st.DeviceUserCode, st.DeviceVerificationURI = fmt.Sprintf("ABCD-%04d", r.Intn(10000)), "https://github.com/login/device"
	}
	for i := r.Intn(30); i > 0; i-- {
		st.Log = append(st.Log, fmt.Sprintf("12:%02d:%02d event %d", r.Intn(60), r.Intn(60), r.Intn(1000)))
	}
	return st
}

// show makes the fake core report st and has the view model read it.
func show(f *fakeCore, vm *ViewModel, st appcore.Status) {
	f.set(func(s *appcore.Status) { *s = st })
	vm.Refresh()
}

// After any run of view-model updates, pointer moves, clicks, theme changes
// and resizes, what a damage-driven host shows equals the full frame, pixel
// for pixel.
func TestIncrementalPresentEqualsAFullRepaint(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		r := rand.New(rand.NewSource(seed))
		f := signedOut()
		f.st = randomStatus(r)
		v, vm := newTestView(t, f)
		var o host
		o.present(v)
		for step := 0; step < 60; step++ {
			switch r.Intn(10) {
			case 0: // a resize, sometimes to the same number of pixels
				w, h := 400+r.Intn(300), 600+r.Intn(300)
				if r.Intn(3) == 0 {
					w, h = h, w
				}
				v.Resize(w, h, 1)
			case 1, 2: // the pointer
				v.MouseMove(r.Intn(v.width), r.Intn(v.high))
			case 3: // a press and release somewhere
				v.MouseDown(r.Intn(v.width), r.Intn(v.high))
				v.MouseUp(r.Intn(v.width), r.Intn(v.high))
			default:
				show(f, vm, randomStatus(r))
			}
			o.present(v)
			if !bytes.Equal(o.shown, v.buf) {
				t.Fatalf("seed %d step %d: the incrementally presented frame differs from a full repaint", seed, step)
			}
		}
		vm.Wait()
	}
}

// Two frames painted before the host asks keep both frames' damage.
func TestDamageAccumulatesAcrossFramesThePresenterSkipped(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	f := signedOut()
	f.st = randomStatus(r)
	v, vm := newTestView(t, f)
	var o host
	o.present(v)
	for i := 0; i < 40; i++ {
		for k := r.Intn(3); k >= 0; k-- { // frames nobody presented
			show(f, vm, randomStatus(r))
			v.Frame()
		}
		o.present(v)
		if !bytes.Equal(o.shown, v.buf) {
			t.Fatalf("round %d: stale pixels after skipped frames", i)
		}
	}
}

// A frame with nothing new to report, and a resize, are the whole frame.
func TestNothingPendingAndAResizeReportTheWholeFrame(t *testing.T) {
	v, _ := newTestView(t, signedIn()) // the first frame was drawn by newTestView
	if got := v.DamageRects(); got != nil {
		t.Fatalf("first frame: %v, want the whole frame (nil)", got)
	}
	if got := v.DamageRects(); got != nil {
		t.Fatalf("no frame pending: %v, want nil", got)
	}
	v.Resize(testW+8, testH, 1)
	v.Frame()
	if got := v.DamageRects(); got != nil {
		t.Fatalf("after a resize: %v, want nil", got)
	}
}

// A change is reported as rectangles, not the whole frame, and not as nothing.
func TestAChangeIsReportedAsRectangles(t *testing.T) {
	f := signedIn()
	v, vm := newTestView(t, f)
	v.DamageRects()
	f.set(func(st *appcore.Status) { st.Log = append(st.Log, "12:00:00 something happened") })
	vm.Refresh()
	if !frame(t, v) {
		t.Fatal("a new log line did not redraw")
	}
	got := v.DamageRects()
	if len(got) == 0 {
		t.Fatal("a changed frame reported the whole frame")
	}
	area := 0
	for _, r := range got {
		area += r.W * r.H
	}
	if area >= testW*testH/2 {
		t.Errorf("a log line damaged %d of %d pixels", area, testW*testH)
	}
}

// BenchmarkPresent measures one frame of a window whose log grows by a line:
// the CPU of painting and finding the damage, and the bytes a host presents.
func BenchmarkPresent(b *testing.B) {
	f := signedIn()
	f.st.Connected = true
	q := &Queue{}
	vm := NewViewModel(f, WithPost(q.Post))
	v := NewView(vm, q)
	defer v.Close()
	vm.Refresh()
	v.Resize(testW, testH, 1)
	var o host
	o.present(v)
	o.bytes, o.rects = 0, 0
	st := f.Status()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Log = append(st.Log, fmt.Sprintf("12:00:%02d event %d", i%60, i))
		if len(st.Log) > 200 {
			st.Log = st.Log[1:]
		}
		show(f, vm, st)
		o.present(v)
	}
	b.ReportMetric(float64(o.bytes)/float64(b.N), "B-presented/frame")
}
