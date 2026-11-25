// SPDX-License-Identifier: BSD-3-Clause
//
// The host: the View as an application.Handler. It owns the framebuffer,
// turns the window's input into toolkit events for the widget tree, and is
// the UI thread's clock -- work posted from other goroutines (the status
// poll, a finished Connect, a tray click) is run at the top of Frame, on the
// thread that draws, which is the only one that touches the ViewModel.

package ui

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-widgets/application"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"
)

// Queue carries functions to the UI thread. Post is safe from any
// goroutine; Drain runs what was posted, in order, on the caller's.
type Queue struct {
	mu      sync.Mutex
	fns     []func()
	pending atomic.Bool
}

// Post queues f for the UI thread.
func (q *Queue) Post(f func()) {
	q.mu.Lock()
	q.fns = append(q.fns, f)
	q.mu.Unlock()
	q.pending.Store(true)
}

// Pending reports whether anything is queued.
func (q *Queue) Pending() bool { return q.pending.Load() }

// Drain runs everything queued, including what those functions post.
func (q *Queue) Drain() {
	for {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.pending.Store(false)
		q.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, f := range fns {
			f()
		}
	}
}

// UseOpenTypeText switches the toolkit to its anti-aliased OpenType face,
// for the whole process; on failure the bitmap face stays.
func UseOpenTypeText() error { return toolkit.UseOpenTypeText() }

// designW and designH are the window the layout is drawn for, in logical
// pixels: the size the application opens at.
const designW, designH = 460, 720

// View is the window: the bound widgets, laid out, as an
// application.Handler.
type View struct {
	vm    *ViewModel
	queue *Queue
	w     *widgets
	cards *cards
	root  toolkit.Widget

	theme       *toolkit.Theme
	buf         []byte
	width, high int
	// repaint says the next frame is whole and damageAll that the whole of it
	// is to be reported. What changed is reported by whoever changed it, not
	// found by scanning pixels: the widgets whose state moved since the last
	// frame, the rectangles painted and not yet reported (damage, and the spare
	// slice it trades places with), the floating popovers drawn in the last
	// frame, the widgets a hover can repaint, and the pointer's last position.
	repaint, damageAll   bool
	touched, interactive []toolkit.Widget
	damage, spareDamage  []toolkit.Rect
	popovers, prevPops   []toolkit.Rect
	left                 []toolkit.Rect // what the cards' pages just left had drawn
	switched             [4]bool        // the card changed page since the last frame
	hoverX, hoverY       int
	hoverStale           bool // widgets moved since the pointer was last seen
	// builtAt is the metric scale the boxes were sized for.
	builtAt float64

	dirty, relayout atomic.Bool
	pressed         bool
	// px, py is where the pointer last was: a wheel scrolls what is under it.
	px, py int
	unbind []func()
}

// NewView binds a View to vm. Its Frame drains queue: give the ViewModel
// WithPost(queue.Post).
func NewView(vm *ViewModel, queue *Queue) *View {
	v := &View{vm: vm, queue: queue, theme: toolkit.DefaultLight()}
	v.w, v.cards, v.unbind = bindView(vm, v.changed, v.cardChanged)
	v.interactive = v.w.interactive()
	v.dirty.Store(true)
	return v
}

// changed is the invalidation of a binder whose state reached ws: the next
// frame reports their rectangles, and only those, as damaged.
func (v *View) changed(ws ...toolkit.Widget) func() {
	return func() {
		v.touched = append(v.touched, ws...)
		v.dirty.Store(true)
	}
}

// cardChanged is the invalidation of card i switching page: what its pages
// drew until now is damaged, and so, once laid out again, is what they draw.
func (v *View) cardChanged(i int) func() {
	return func() {
		for _, p := range v.cards.pages[i] {
			v.left = append(v.left, extent(p))
		}
		v.switched[i], v.hoverStale = true, true
		v.relayout.Store(true)
		v.dirty.Store(true)
	}
}

// everything makes the next frame whole: a change nothing here can bound.
func (v *View) everything() {
	v.repaint = true
	v.dirty.Store(true)
}

// hovered marks the interactive widgets under (x, y), whose hover feedback a
// pointer move there changes.
// (x, y) is in framebuffer pixels; the toolkit is handed it relative to the
// root, so a widget is taken as under the pointer in either frame of reference.
func (v *View) hovered(x, y int) {
	r := v.root.Bounds()
	for _, w := range v.interactive {
		if b := w.Bounds(); b.Contains(x, y) || b.Contains(x-r.X, y-r.Y) {
			v.touched = append(v.touched, w)
			v.dirty.Store(true)
		}
	}
}

// Close releases the bindings.
func (v *View) Close() {
	for _, u := range v.unbind {
		u()
	}
	v.unbind = nil
}

// Resize is told the framebuffer's size in device pixels.
func (v *View) Resize(w, h int, _ float64) {
	if w <= 0 || h <= 0 {
		return
	}
	v.width, v.high = w, h
	v.buf = make([]byte, 4*w*h)
	v.repaint, v.hoverStale = true, true
	v.relayout.Store(true)
	v.dirty.Store(true)
}

// arrange (re)builds the boxes when the scale changed, and gives the tree
// its bounds.
func (v *View) arrange() {
	if s := toolkit.MetricScale(); v.root == nil || s != v.builtAt {
		v.root, v.builtAt = layout(v.w, v.cards), s
		v.repaint = true
	}
	m := toolkit.Scaled(margin)
	v.root.SetBounds(toolkit.Rect{X: m, Y: m, W: v.width - 2*m, H: v.high - 2*m})
}

// Frame runs what was posted to the UI thread, then draws if anything
// changed.
func (v *View) Frame() ([]byte, int, int, bool) {
	v.queue.Drain()
	if v.buf == nil {
		return nil, 0, 0, false
	}
	if v.relayout.Swap(false) || v.root == nil {
		v.arrange()
	}
	if !v.dirty.Swap(false) {
		return v.buf, v.width, v.high, false
	}
	bg := v.theme.Background
	for i := 0; i+3 < len(v.buf); i += 4 {
		v.buf[i], v.buf[i+1], v.buf[i+2], v.buf[i+3] = bg.R, bg.G, bg.B, bg.A
	}
	p := painter.NewPixelPainter(v.buf, v.width, v.high)
	v.root.Draw(p, v.theme)
	v.collectDamage()
	return v.buf, v.width, v.high, true
}

// collectDamage turns what the binders and events reported into rectangles,
// now that the layout they sit in is the one just painted. A popover floats
// outside its widget, so the ones drawn now and the ones drawn last frame are
// both damaged: it covers pixels it did not cover before, or uncovers them.
func (v *View) collectDamage() {
	pops := v.popovers[:0]
	for _, o := range toolkit.PopoverOwners(v.root) {
		pops = append(pops, o.PopoverBounds())
	}
	// The widgets report what they changed within their own rectangles, and
	// that holds in the window the layout is drawn for. Below its size a
	// control squeezed past its content draws outside itself, which no
	// rectangle bounds, so such a window is repainted whole.
	if v.width < toolkit.Scaled(designW) || v.high < toolkit.Scaled(designH) {
		v.repaint = true
	}
	if v.repaint {
		v.damageAll, v.repaint = true, false
	}
	if !v.damageAll {
		for _, w := range v.touched {
			v.addDamage(w.Bounds())
		}
		for _, r := range pops {
			v.addDamage(r)
		}
		for _, r := range v.prevPops {
			v.addDamage(r)
		}
		for i, cl := range v.cards.layouts() {
			if a, pages := cl.Active, v.cards.pages[i]; v.switched[i] && a >= 0 && a < len(pages) {
				v.addDamage(extent(pages[a]))
			}
		}
		for _, r := range v.left {
			v.addDamage(r)
		}
	}
	v.switched = [4]bool{}
	v.left = v.left[:0]
	clear(v.touched) // drop the widget references, keep the room
	v.touched = v.touched[:0]
	v.popovers, v.prevPops = v.prevPops[:0], pops
}

// extent is the rectangle w and everything under it occupies: a box lays its
// children out in more room than it has when the window is short, and they
// draw there.
func extent(w toolkit.Widget) toolkit.Rect {
	r := w.Bounds()
	if cp, ok := w.(interface{ Children() []toolkit.Widget }); ok {
		for _, c := range cp.Children() {
			r = union(r, extent(c))
		}
	}
	return r
}

// union is the smallest rectangle holding a and b; an empty one is ignored.
func union(a, b toolkit.Rect) toolkit.Rect {
	switch {
	case a.W <= 0 || a.H <= 0:
		return b
	case b.W <= 0 || b.H <= 0:
		return a
	}
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return toolkit.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// addDamage records r, cut to the frame: a widget can sit partly or wholly
// outside it, and a host copies exactly the rectangles it is given.
func (v *View) addDamage(r toolkit.Rect) {
	x0, y0 := max(r.X, 0), max(r.Y, 0)
	x1, y1 := min(r.X+r.W, v.width), min(r.Y+r.H, v.high)
	if x1 > x0 && y1 > y0 {
		v.damage = append(v.damage, toolkit.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0})
	}
}

// DamageRects reports the rectangles, in framebuffer pixels, that changed
// since the last call: what the window has to re-present instead of the whole
// frame. They come from what changed (the widgets whose state moved, the
// widget under a hovering pointer), not from comparing pixels. It reports
// nothing (the whole frame) after a resize, a theme change or an input the
// view cannot bound, and whenever no painted frame is pending, so a host that
// presents for any other reason, an expose for one, repaints everything and is
// never left with stale pixels. The slice is valid until the next call.
func (v *View) DamageRects() []toolkit.Rect {
	out := v.damage
	if v.damageAll || len(out) == 0 {
		out = nil
	}
	v.damage, v.spareDamage = v.spareDamage[:0], v.damage // two slices trade places: no allocation per frame
	v.damageAll = false
	return out
}

// NeedsPresent, PresentImmediate and PresentThrottle let the window skip
// the blit when nothing changed and nothing is queued.
func (v *View) NeedsPresent() bool     { return v.dirty.Load() || v.relayout.Load() || v.queue.Pending() }
func (v *View) PresentImmediate() bool { return v.NeedsPresent() }
func (v *View) PresentThrottle() bool  { return false }

// event hands an input event to the tree, in the root's coordinates.
func (v *View) event(ev toolkit.Event) {
	if v.root == nil {
		return
	}
	r := v.root.Bounds()
	ev.X -= r.X
	ev.Y -= r.Y
	v.root.OnEvent(ev)
	if ev.Kind == toolkit.EventMouseMove && !v.pressed {
		// A hover repaints the widget the pointer left and the one it entered,
		// and a popover list highlights the row under it. Where the widgets have
		// moved since the pointer was last seen (a resize, a page change), a
		// hover flag kept where one used to be flips wherever that widget is
		// now, so every one that can hover is repainted once.
		if v.hoverStale {
			v.touched = append(v.touched, v.interactive...)
			v.hoverStale = false
		}
		v.hovered(v.hoverX, v.hoverY)
		v.hovered(ev.X+r.X, ev.Y+r.Y)
		v.dirty.Store(true)
	} else {
		// A press moves focus, a key edits or moves it, a wheel scrolls, a drag
		// selects: nothing here says where, so the frame is whole. Nor where a
		// button now believes the pointer is: a press, a drag and a release set
		// that without a plain move.
		v.everything()
		v.hoverStale = true
	}
	v.hoverX, v.hoverY = ev.X+r.X, ev.Y+r.Y
}

// MouseDown is a click: the toolkit acts on the press.
func (v *View) MouseDown(x, y int) {
	v.pressed, v.px, v.py = true, x, y
	v.event(toolkit.Event{Kind: toolkit.EventClick, X: x, Y: y})
}

// MouseMove is a drag while the button is held (a scrollbar thumb), a hover
// otherwise.
func (v *View) MouseMove(x, y int) {
	v.px, v.py = x, y
	kind := toolkit.EventMouseMove
	if v.pressed {
		kind = toolkit.EventMouseDrag
	}
	v.event(toolkit.Event{Kind: kind, X: x, Y: y})
}

func (v *View) MouseUp(x, y int) {
	v.pressed = false
	v.event(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
}

// wheelPixelsPerRow is application's pixels per wheel notch.
const wheelPixelsPerRow = 40

// Scroll is a wheel delta in device pixels; the toolkit counts rows. The
// application moves the pointer to the wheel's position just before.
func (v *View) Scroll(dy int) {
	rows := dy / wheelPixelsPerRow
	if rows == 0 && dy != 0 {
		rows = 1
		if dy < 0 {
			rows = -1
		}
	}
	v.event(toolkit.Event{Kind: toolkit.EventScroll, X: v.px, Y: v.py, Delta: rows})
}

// keyNames maps the application's key names back to the toolkit's.
var keyNames = map[string]string{"Up": "ArrowUp", "Down": "ArrowDown", "Left": "ArrowLeft", "Right": "ArrowRight"}

// Key is a named key or a typed character.
func (v *View) Key(name string, r rune) {
	if name == "" {
		v.event(toolkit.Event{Kind: toolkit.EventChar, Code: string(r)})
		return
	}
	if k, ok := keyNames[name]; ok {
		name = k
	}
	v.event(toolkit.Event{Kind: toolkit.EventKeyDown, Code: name})
}

// Shortcut is a Ctrl chord: Ctrl+V pastes a client id into a field.
func (v *View) Shortcut(r rune, ctrl, meta bool) {
	if !ctrl && !meta {
		return
	}
	v.event(toolkit.Event{Kind: toolkit.EventKeyDown, Code: "Ctrl+" + strings.ToUpper(string(r)), Ctrl: ctrl, Meta: meta})
}

// SystemAppearance follows the system's light or dark mode.
func (v *View) SystemAppearance(a application.SystemAppearance) {
	if a.Dark {
		v.theme = toolkit.DefaultDark()
	} else {
		v.theme = toolkit.DefaultLight()
	}
	v.everything()
}

// NativeControls describes the native controls the host embeds: one edit
// control per text input on the page being shown. It runs on the UI thread, as
// do the callbacks it returns.
func (v *View) NativeControls() []toolkit.NativeControl {
	cs := toolkit.WalkNative(v.root)
	for i := range cs {
		if f := cs[i].OnText; f != nil {
			cs[i].OnText = func(s string) { f(s); v.everything() }
		}
		if f := cs[i].OnClaim; f != nil {
			cs[i].OnClaim = func(b bool) { f(b); v.everything() }
		}
	}
	return cs
}

// A11yElements describes the painted widgets to the platform's accessibility
// layer (UI Automation): role, label and bounds of each, so a screen reader or
// an automation client sees the buttons the toolkit draws.
func (v *View) A11yElements() []application.A11yElement {
	return application.A11yElementsOf(v.root)
}

var (
	_ application.Accessible            = (*View)(nil)
	_ application.NativeControlProvider = (*View)(nil)
	_ application.Handler               = (*View)(nil)
	_ application.ShortcutSink          = (*View)(nil)
	_ application.AppearanceSink        = (*View)(nil)
)
