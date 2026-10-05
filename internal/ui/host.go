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
	// builtAt is the metric scale the boxes were sized for.
	builtAt float64

	dirty, relayout atomic.Bool
	pressed         bool
	// px, py is where the pointer last was: a wheel scrolls what is under it.
	px, py int
	unbind []func()
}

// NewView binds a View to vm. Its Frame drains queue: give the ViewModel
// WithDispatch(spawn, queue.Post).
func NewView(vm *ViewModel, queue *Queue) *View {
	v := &View{vm: vm, queue: queue, theme: toolkit.DefaultLight()}
	invalidate := func() { v.dirty.Store(true) }
	relayout := func() { v.relayout.Store(true) }
	v.w, v.cards, v.unbind = bindView(vm, invalidate, relayout)
	v.dirty.Store(true)
	return v
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
	v.relayout.Store(true)
	v.dirty.Store(true)
}

// arrange (re)builds the boxes when the scale changed, and gives the tree
// its bounds.
func (v *View) arrange() {
	if s := toolkit.MetricScale(); v.root == nil || s != v.builtAt {
		v.root, v.builtAt = layout(v.w, v.cards), s
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
	return v.buf, v.width, v.high, true
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
	v.dirty.Store(true)
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
	v.dirty.Store(true)
}

var (
	_ application.Handler        = (*View)(nil)
	_ application.ShortcutSink   = (*View)(nil)
	_ application.AppearanceSink = (*View)(nil)
)
