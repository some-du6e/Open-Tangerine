package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const version = "0.1.0"

const (
	wmTray    = wmApp + 1
	wmDone    = wmApp + 2
	idPause   = 1
	idStartup = 2
	idQuit    = 3
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// Drags may start from Explorer windows or the Desktop only.
var sourceClasses = map[string]bool{"CabinetWClass": true, "ExploreWClass": true, "Progman": true, "WorkerW": true}

type app struct {
	tray, layer windows.HWND
	icon        windows.Handle
	taskbar     uint32 // "TaskbarCreated": Explorer restarted, re-add the icon
	paused      bool
	start       time.Time

	// The catch layer, on whichever monitor the drag is on.
	t       thrower
	monX    int32
	monY    int32
	scale   float64
	memDC   uintptr
	bitmap  uintptr
	canvas  *canvas
	shown   bool
	lastNow float64

	// Mouse tracking for the current button press.
	pressed     bool
	pressAt     float64
	pressX      float64
	pressY      float64
	fromSource  bool
	releasedAt  float64
	moveSamples []sample

	mu      sync.Mutex
	results []result // finished conversions waiting for a balloon
	lastOut string
}

type result struct {
	text string
	warn bool
}

var a = &app{scale: 1}
var target = &dropTarget{vtbl: &dropVtbl, handler: a}

func main() {
	runtime.LockOSThread()
	a.start = time.Now()
	pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // per-monitor v2
	if _, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(utf16(`Local\TossThrowToConvert`)))); errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return
	}
	if hr, _, _ := pOleInitialize.Call(0); int32(hr) < 0 {
		messageBox("Toss couldn't start drag and drop (OleInitialize failed).")
		return
	}
	instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	arrow, _, _ := pLoadCursorW.Call(0, 32512)
	register := func(name string, proc uintptr) {
		wc := wndClassEx{wndProc: proc, instance: windows.Handle(instance), cursor: windows.Handle(arrow), className: utf16(name)}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	}
	proc := windows.NewCallback(wndProc)
	register("TossTray", proc)
	register("TossLayer", proc)
	create := func(class string, exStyle, style uintptr) windows.HWND {
		h, _, _ := pCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16("Toss"))),
			style, 0, 0, 0, 0, 0, 0, instance, 0)
		return windows.HWND(h)
	}
	a.tray = create("TossTray", 0, 0)
	a.layer = create("TossLayer", wsExLayered|wsExToolWindow|wsExTopmost|wsExNoActivate, wsPopup)
	if a.tray == 0 || a.layer == 0 {
		messageBox("Toss couldn't create its windows.")
		return
	}
	if hr, _, _ := pRegisterDragDrop.Call(uintptr(a.layer), uintptr(unsafe.Pointer(target))); hr != 0 {
		messageBox(fmt.Sprintf("Toss couldn't register for drops (0x%x).", hr))
		return
	}
	a.taskbar = registerMessage("TaskbarCreated")
	a.icon = makeIcon(32)
	a.trayIcon(nimAdd)
	pSetTimer.Call(uintptr(a.tray), 1, 15, 0)

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	a.trayIcon(nimDelete)
}

func registerMessage(name string) uint32 {
	r, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(utf16(name))))
	return uint32(r)
}

func (a *app) now() float64 { return time.Since(a.start).Seconds() }

func wndProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	switch {
	case message == wmMouseActivate:
		return maNoActivate
	case hwnd != a.tray:
	case message == wmTimer:
		a.poll()
		a.animate()
		return 0
	case message == wmTray:
		switch uint32(lParam) & 0xFFFF {
		case wmRButtonUp, wmLButtonUp:
			a.menu()
		case ninBalloonClk:
			a.reveal()
		}
		return 0
	case message == wmDone:
		a.mu.Lock()
		results := a.results
		a.results = nil
		a.mu.Unlock()
		for _, r := range results {
			a.balloon(r)
		}
		return 0
	case message == a.taskbar && a.taskbar != 0:
		a.trayIcon(nimAdd)
		return 0
	case message == wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

// poll watches the mouse: a fast swing during an Explorer drag arms the layer.
// It reads button state and cursor position only, never keys or text.
func (a *app) poll() {
	now := a.now()
	button := uintptr(vkLButton)
	if swapped, _, _ := pGetSystemMetrics.Call(23); swapped != 0 {
		button = vkRButton
	}
	down := held(button) && !held(vkEscape) && !a.paused
	if !down {
		if a.pressed {
			a.pressed, a.releasedAt, a.moveSamples = false, now, nil
		}
		// The drop arrives just after the button comes up; give it a moment.
		if a.t.catching() && now-a.releasedAt > 0.25 {
			a.t.disarm(now)
		}
		return
	}
	px, py := cursorPos()
	x, y := float64(px)/a.scale, float64(py)/a.scale
	if !a.pressed {
		a.pressed, a.pressAt, a.pressX, a.pressY = true, now, x, y
		a.fromSource = sourceClasses[foregroundClass()]
	}
	if !a.fromSource {
		return
	}
	a.moveSamples = append(a.moveSamples, sample{now, x, y})
	if len(a.moveSamples) > 16 {
		a.moveSamples = a.moveSamples[1:]
	}
	vx, vy := velocity(a.moveSamples, now)
	speed := math.Hypot(vx, vy)
	if a.t.state == idle {
		moved := math.Abs(x-a.pressX) + math.Abs(y-a.pressY)
		if now-a.pressAt > 0.12 && moved > 40 && speed > armSpeed {
			a.arm(px, py, now)
		}
		return
	}
	a.t.sample(float64(px-a.monX)/a.scale, float64(py-a.monY)/a.scale, now)
	a.t.watch(speed, now)
}

// arm covers the monitor under the cursor with the invisible catch layer.
func (a *app) arm(px, py int32, now float64) {
	mon, _, _ := pMonitorFromPoint.Call(uintptr(uint32(px))|uintptr(uint32(py))<<32, 2)
	info := monitorInfo{}
	info.cbSize = uint32(unsafe.Sizeof(info))
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&info)))
	var dx, dy uint32 = 96, 96
	pGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy)))
	a.scale = float64(dx) / 96
	r, w := info.monitor, info.work
	a.monX, a.monY = r.left, r.top
	width, height := int(r.right-r.left), int(r.bottom-r.top)
	if a.canvas == nil || a.canvas.w != width || a.canvas.h != height {
		a.newSurface(width, height)
	}
	area := rect{float64(w.left-r.left) / a.scale, float64(w.top-r.top) / a.scale,
		float64(w.right-r.left) / a.scale, float64(w.bottom-r.top) / a.scale}
	// Re-express the swing so far in this monitor's local coordinates.
	a.t.samples = nil
	for _, s := range a.moveSamples {
		a.t.sample(s.x-float64(a.monX)/a.scale, s.y-float64(a.monY)/a.scale, s.t)
	}
	a.t.arm(area, now)
	a.present(now)
}

func (a *app) newSurface(w, h int) {
	if a.bitmap != 0 {
		pDeleteObject.Call(a.bitmap)
		pDeleteDC.Call(a.memDC)
	}
	screen, _, _ := pGetDC.Call(0)
	a.memDC, _, _ = pCreateCompatibleDC.Call(screen)
	pReleaseDC.Call(0, screen)
	header := struct {
		h     bitmapInfoHeader
		color uint32
	}{h: bitmapInfoHeader{width: int32(w), height: -int32(h), planes: 1, bitCount: 32}}
	header.h.size = uint32(unsafe.Sizeof(header.h))
	var bits unsafe.Pointer
	a.bitmap, _, _ = pCreateDIBSection.Call(a.memDC, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	pSelectObject.Call(a.memDC, a.bitmap)
	a.canvas = newCanvas(w, h, unsafe.Slice((*byte)(bits), w*h*4))
}

// present draws the current frame and shows or hides the layer to match.
func (a *app) present(now float64) {
	if a.t.state == idle {
		if a.shown {
			pShowWindow.Call(uintptr(a.layer), swHide)
			a.shown = false
		}
		a.t.full = true
		return
	}
	dirty := a.t.render(a.canvas, a.scale, now)
	if dirty.Empty() && a.shown {
		return
	}
	dst := winPoint{a.monX, a.monY}
	size := winPoint{int32(a.canvas.w), int32(a.canvas.h)}
	src := winPoint{}
	blend := blendFunction{alpha: 255, format: 1}
	d := winRect{int32(dirty.Min.X), int32(dirty.Min.Y), int32(dirty.Max.X), int32(dirty.Max.Y)}
	info := updateLayeredWindowInfo{ptDst: &dst, size: &size, hdcSrc: a.memDC, ptSrc: &src, blend: &blend, flags: ulwAlpha, dirty: &d}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if !a.shown {
		info.dirty = nil // the first frame must cover the whole surface
	}
	pUpdateLayeredWindowIndirect.Call(uintptr(a.layer), uintptr(unsafe.Pointer(&info)))
	if !a.shown {
		pSetWindowPos.Call(uintptr(a.layer), hwndTopmost, uintptr(a.monX), uintptr(a.monY),
			uintptr(a.canvas.w), uintptr(a.canvas.h), swpNoActivate)
		pShowWindow.Call(uintptr(a.layer), swShowNoActivate)
		a.shown = true
	}
}

func (a *app) animate() {
	now := a.now()
	dt := min(now-a.lastNow, 0.05)
	a.lastNow = now
	if j := a.t.tick(dt, now); j != nil {
		go a.run(*j)
	}
	if a.t.state != idle || a.shown {
		a.present(now)
	}
}

// Drop target callbacks, from OLE on the UI thread. Coordinates are physical screen pixels.

func (a *app) local(x, y int32) (float64, float64) {
	return float64(x-a.monX) / a.scale, float64(y-a.monY) / a.scale
}

func (a *app) dragEnter(paths []string, allowed uint32, x, y int32) uint32 {
	formats := targets(files(paths), ffmpegPath() != "")
	lx, ly := a.local(x, y)
	if allowed&dropEffectCopy == 0 || !a.t.enter(paths, formats, lx, ly, a.now()) {
		// Not something we convert: step aside so Explorer gets the drop.
		a.t.disarm(a.now())
		a.present(a.now())
		return dropEffectNone
	}
	a.present(a.now())
	return dropEffectCopy
}

func (a *app) dragOver(x, y int32) uint32 {
	if a.t.state != aiming {
		return dropEffectNone
	}
	lx, ly := a.local(x, y)
	a.t.over(lx, ly, a.now())
	return dropEffectCopy
}

func (a *app) dragLeave() {
	a.t.disarm(a.now())
	a.present(a.now())
}

func (a *app) dropped(x, y int32) uint32 {
	if a.t.state != aiming {
		return dropEffectNone
	}
	lx, ly := a.local(x, y)
	a.t.drop(lx, ly, a.now())
	a.lastNow = a.now()
	return dropEffectCopy
}

// files drops anything that isn't a regular file, so folders never get converted.
func files(paths []string) []string {
	for _, p := range paths {
		if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
			return nil
		}
	}
	return paths
}

func (a *app) run(j job) {
	outs, err := convert(j.paths, j.target)
	r := result{warn: err != nil}
	switch {
	case err != nil && len(outs) == 0:
		r.text = "Couldn't convert: " + err.Error()
	case err != nil:
		r.text = fmt.Sprintf("Made %d file(s), some failed: %v", len(outs), err)
	case len(outs) == 1:
		r.text = "Saved " + filepath.Base(outs[0])
	default:
		r.text = fmt.Sprintf("Saved %d files", len(outs))
	}
	a.mu.Lock()
	a.results = append(a.results, r)
	if len(outs) > 0 {
		a.lastOut = outs[len(outs)-1]
	}
	a.mu.Unlock()
	pPostMessageW.Call(uintptr(a.tray), wmDone, 0, 0)
}

// reveal opens Explorer with the last converted file selected.
func (a *app) reveal() {
	a.mu.Lock()
	out := a.lastOut
	a.mu.Unlock()
	if out != "" {
		exec.Command("explorer.exe", "/select,", out).Start()
	}
}

func (a *app) iconData() notifyIconData {
	d := notifyIconData{hwnd: a.tray, id: 1, flags: nifMessage | nifIcon | nifTip, callbackMessage: wmTray, icon: a.icon}
	d.cbSize = uint32(unsafe.Sizeof(d))
	tip := "Toss · fling a dragged file into a bin"
	if a.paused {
		tip = "Toss · paused"
	}
	copyUTF16(d.tip[:], tip)
	return d
}

func (a *app) trayIcon(action uintptr) {
	d := a.iconData()
	pShellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&d)))
}

func (a *app) balloon(r result) {
	d := a.iconData()
	d.flags |= nifInfo
	d.infoFlags = niifInfo
	if r.warn {
		d.infoFlags = niifWarning
	}
	copyUTF16(d.infoTitle[:], "Toss")
	copyUTF16(d.info[:], r.text)
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

func (a *app) menu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(id uintptr, label string, checked bool) {
		flags := uintptr(mfString)
		if checked {
			flags |= mfChecked
		}
		pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(utf16(label))))
	}
	pAppendMenuW.Call(m, mfString|0x1 /* grayed */, 0, uintptr(unsafe.Pointer(utf16("Toss "+version))))
	pAppendMenuW.Call(m, mfSeparator, 0, 0)
	add(idPause, "Pause", a.paused)
	add(idStartup, "Start with Windows", startsWithWindows())
	pAppendMenuW.Call(m, mfSeparator, 0, 0)
	add(idQuit, "Quit", false)
	x, y := cursorPos()
	pSetForegroundWindow.Call(uintptr(a.tray)) // so the menu closes when you click away
	id, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmRightBtn, uintptr(x), uintptr(y), 0, uintptr(a.tray), 0)
	switch id {
	case idPause:
		a.paused = !a.paused
		a.t.disarm(a.now())
		a.present(a.now())
		a.trayIcon(nimModify)
	case idStartup:
		setStartsWithWindows(!startsWithWindows())
	case idQuit:
		pPostQuitMessage.Call(0)
	}
}

func startsWithWindows() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Toss")
	return err == nil
}

func setStartsWithWindows(on bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if !on {
		k.DeleteValue("Toss")
		return
	}
	if exe, err := os.Executable(); err == nil {
		k.SetStringValue("Toss", `"`+exe+`"`)
	}
}

// makeIcon draws the tray icon: an orange ball mid-throw.
func makeIcon(size int) windows.Handle {
	c := newCanvas(size, size, nil)
	s := float64(size) / 32
	c.disc(9*s, 23*s, 4*s, fade(orange, 0.35), fade(orange, 0.35))
	c.disc(20*s, 13*s, 11*s, orangeLight, orange)
	header := struct {
		h     bitmapInfoHeader
		color uint32
	}{h: bitmapInfoHeader{width: int32(size), height: -int32(size), planes: 1, bitCount: 32}}
	header.h.size = uint32(unsafe.Sizeof(header.h))
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	var bits unsafe.Pointer
	color, _, _ := pCreateDIBSection.Call(screen, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	copy(unsafe.Slice((*byte)(bits), size*size*4), c.pix)
	mask, _, _ := pCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	info := iconInfo{isIcon: 1, mask: windows.Handle(mask), color: windows.Handle(color)}
	icon, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	pDeleteObject.Call(color)
	pDeleteObject.Call(mask)
	return windows.Handle(icon)
}
