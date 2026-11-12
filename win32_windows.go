package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pSetTimer                      = user32.NewProc("SetTimer")
	pGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	pGetClassNameW                 = user32.NewProc("GetClassNameW")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pUpdateLayeredWindowIndirect   = user32.NewProc("UpdateLayeredWindowIndirect")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pMonitorFromPoint              = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pCreateIconIndirect            = user32.NewProc("CreateIconIndirect")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pCreateBitmap       = gdi32.NewProc("CreateBitmap")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pDragQueryFileW   = shell32.NewProc("DragQueryFileW")

	pOleInitialize    = ole32.NewProc("OleInitialize")
	pRegisterDragDrop = ole32.NewProc("RegisterDragDrop")
	pReleaseStgMedium = ole32.NewProc("ReleaseStgMedium")

	pGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")
)

const (
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmTimer         = 0x0113
	wmMouseActivate = 0x0021
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	maNoActivate    = 3

	wsPopup          = 0x80000000
	wsExTopmost      = 0x00000008
	wsExToolWindow   = 0x00000080
	wsExLayered      = 0x00080000
	wsExNoActivate   = 0x08000000
	swHide           = 0
	swShowNoActivate = 4
	hwndTopmost      = ^uintptr(0) // (HWND)-1
	swpNoActivate    = 0x0010

	vkLButton = 0x01
	vkRButton = 0x02
	vkEscape  = 0x1B

	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nifMessage    = 0x1
	nifIcon       = 0x2
	nifTip        = 0x4
	nifInfo       = 0x10
	niifInfo      = 0x1
	niifWarning   = 0x2
	ninBalloonClk = 0x405

	mfString     = 0x0
	mfSeparator  = 0x800
	mfChecked    = 0x8
	tpmReturnCmd = 0x100
	tpmRightBtn  = 0x2

	ulwAlpha = 0x2
)

type wndClassEx struct {
	cbSize     uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type msg struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
	private uint32
}

type winPoint struct{ x, y int32 }

type winRect struct{ left, top, right, bottom int32 }

type monitorInfo struct {
	cbSize  uint32
	monitor winRect
	work    winRect
	flags   uint32
}

type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPPM, yPPM    int32
	clrUsed       uint32
	clrImportant  uint32
}

type blendFunction struct{ op, flags, alpha, format byte }

type updateLayeredWindowInfo struct {
	cbSize uint32
	hdcDst uintptr
	ptDst  *winPoint
	size   *winPoint
	hdcSrc uintptr
	ptSrc  *winPoint
	key    uint32
	blend  *blendFunction
	flags  uint32
	dirty  *winRect
}

type notifyIconData struct {
	cbSize          uint32
	hwnd            windows.HWND
	id              uint32
	flags           uint32
	callbackMessage uint32
	icon            windows.Handle
	tip             [128]uint16
	state           uint32
	stateMask       uint32
	info            [256]uint16
	version         uint32
	infoTitle       [64]uint16
	infoFlags       uint32
	guid            windows.GUID
	balloonIcon     windows.Handle
}

type iconInfo struct {
	isIcon   int32
	xHotspot uint32
	yHotspot uint32
	mask     windows.Handle
	color    windows.Handle
}

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyUTF16(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)-1]
		u = append(u, 0)
	}
	copy(dst, u)
}

func held(key uintptr) bool {
	r, _, _ := pGetAsyncKeyState.Call(key)
	return r&0x8000 != 0
}

func cursorPos() (int32, int32) {
	var p winPoint
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p.x, p.y
}

func foregroundClass() string {
	hwnd, _, _ := pGetForegroundWindow.Call()
	var buf [256]uint16
	pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	return syscall.UTF16ToString(buf[:])
}

func messageBox(text string) {
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16("Toss"))), 0x10)
}
