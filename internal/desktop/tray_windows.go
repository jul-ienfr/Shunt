//go:build windows

package desktop

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmContextMenu   = 0x007B
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	wmTray          = wmApp + 1
	wmNull          = 0x0000

	niMessage = 0x00000001
	niIcon    = 0x00000002
	niTip     = 0x00000004
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfChecked   = 0x00000008
	mfGrayed    = 0x00000001

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002

	idiApplication = 32512
	idcArrow       = 32512

	csHRedraw = 0x0001
	csVRedraw = 0x0002

	wsOverlapped = 0x00000000
	cwUseDefault = 0x80000000

	idOpen        = 1
	idCopy        = 2
	idQuit        = 3
	idProfileBase = 100

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	errorClassAlreadyExists = 1410
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	ntdll    = syscall.NewLazyDLL("ntdll.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procOpenClipboard       = user32.NewProc("OpenClipboard")
	procEmptyClipboard      = user32.NewProc("EmptyClipboard")
	procSetClipboardData    = user32.NewProc("SetClipboardData")
	procCloseClipboard      = user32.NewProc("CloseClipboard")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc         = kernel32.NewProc("GlobalAlloc")
	procGlobalLock          = kernel32.NewProc("GlobalLock")
	procGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	procGlobalFree          = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory       = ntdll.NewProc("RtlMoveMemory")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd     syscall.Handle
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

// notifyIconData mirrors the full NOTIFYICONDATAW layout (V3/full size), so
// Windows accepts cbSize on both 32-bit and 64-bit builds.
type notifyIconData struct {
	Size             uint32
	Wnd              syscall.Handle
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             syscall.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUID             [16]byte
	BalloonIcon      syscall.Handle
}

type trayInstance struct {
	opts   TrayOptions
	hwnd   syscall.Handle
	icon   syscall.Handle
	logger *slog.Logger
}

var (
	trayMu     sync.Mutex
	activeTray *trayInstance
)

func setActive(t *trayInstance) {
	trayMu.Lock()
	activeTray = t
	trayMu.Unlock()
}

func currentTray() *trayInstance {
	trayMu.Lock()
	defer trayMu.Unlock()
	return activeTray
}

func wndProc(hwnd uintptr, message uintptr, wParam, lParam uintptr) uintptr {
	t := currentTray()
	if t == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
		return ret
	}
	switch uint32(message) {
	case wmTray:
		switch lParam {
		case wmRButtonUp, wmContextMenu:
			showMenu(t)
		case wmLButtonDblClk:
			openDashboard(t)
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(uintptr(t.hwnd))
		return 0
	case wmDestroy:
		removeIcon(syscall.Handle(hwnd))
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return ret
}

// RunTray creates the tray icon and runs its message loop until the context is
// canceled or the user chooses Quit. It is safe to call on a goroutine.
func RunTray(ctx context.Context, opts TrayOptions) error {
	if opts.URL == nil || opts.OnOpen == nil {
		return fmt.Errorf("desktop: tray requires URL and OnOpen callbacks")
	}
	if opts.Title == "" {
		opts.Title = "Shunt"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	if hInstance == 0 {
		return fmt.Errorf("desktop: GetModuleHandleW failed")
	}
	className, err := syscall.UTF16PtrFromString("ShuntTrayHost")
	if err != nil {
		return err
	}
	icon, _, iconErr := procLoadIconW.Call(0, idiApplication)
	if icon == 0 {
		return fmt.Errorf("desktop: LoadIconW failed: %v", iconErr)
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)

	wc := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		Style:      csHRedraw | csVRedraw,
		WndProc:    syscall.NewCallback(wndProc),
		Instance:   syscall.Handle(hInstance),
		Icon:       syscall.Handle(icon),
		Cursor:     syscall.Handle(cursor),
		Background: 0,
		ClassName:  className,
	}
	atom, _, regErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		if errno, ok := regErr.(syscall.Errno); !ok || errno != errorClassAlreadyExists {
			return fmt.Errorf("desktop: RegisterClassExW failed: %v", regErr)
		}
	}

	hwnd, _, createErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		wsOverlapped,
		uintptr(cwUseDefault),
		uintptr(cwUseDefault),
		0,
		0,
		0,
		0,
		hInstance,
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("desktop: CreateWindowExW failed: %v", createErr)
	}

	t := &trayInstance{
		opts:   opts,
		hwnd:   syscall.Handle(hwnd),
		icon:   syscall.Handle(icon),
		logger: opts.Logger,
	}
	setActive(t)
	defer setActive(nil)

	if err := addIcon(t); err != nil {
		procDestroyWindow.Call(uintptr(t.hwnd))
		return err
	}
	defer removeIcon(t.hwnd)

	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				procPostMessageW.Call(uintptr(t.hwnd), wmClose, 0, 0)
				return
			case <-ticker.C:
				updateTooltip(t)
			case <-done:
				return
			}
		}
	}()

	var m msg
	for {
		ret, _, getErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("desktop: GetMessageW failed: %v", getErr)
		}
		if ret == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func addIcon(t *trayInstance) error {
	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Wnd:             t.hwnd,
		ID:              1,
		Flags:           niMessage | niIcon | niTip,
		CallbackMessage: wmTray,
		Icon:            t.icon,
	}
	setTip(&nid.Tip, tooltip(t))
	ret, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ret == 0 {
		return fmt.Errorf("desktop: Shell_NotifyIcon(NIM_ADD) failed: %v", err)
	}
	return nil
}

func removeIcon(hwnd syscall.Handle) {
	nid := notifyIconData{
		Size: uint32(unsafe.Sizeof(notifyIconData{})),
		Wnd:  hwnd,
		ID:   1,
	}
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func updateTooltip(t *trayInstance) {
	nid := notifyIconData{
		Size:  uint32(unsafe.Sizeof(notifyIconData{})),
		Wnd:   t.hwnd,
		ID:    1,
		Flags: niTip,
	}
	setTip(&nid.Tip, tooltip(t))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func tooltip(t *trayInstance) string {
	text := t.opts.Title
	if t.opts.Status != nil {
		if status := t.opts.Status(); status != "" {
			text += " - " + status
		}
	}
	return text
}

func setTip(dst *[128]uint16, text string) {
	u16, err := syscall.UTF16FromString(text)
	if err != nil {
		u16 = []uint16{0}
	}
	n := len(u16)
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst[:n], u16[:n])
	if n == len(dst) {
		dst[len(dst)-1] = 0
	}
}

func showMenu(t *trayInstance) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendMenu(menu, mfString, idOpen, "Open dashboard")
	appendMenu(menu, mfString, idCopy, "Copy dashboard URL")
	appendMenu(menu, mfSeparator, 0, "")
	if t.opts.Status != nil {
		appendMenu(menu, mfString|mfGrayed, 0, t.opts.Status())
		appendMenu(menu, mfSeparator, 0, "")
	}

	current := ""
	if t.opts.Current != nil {
		current = t.opts.Current()
	}
	if t.opts.Profiles != nil {
		for i, p := range t.opts.Profiles() {
			flags := uintptr(mfString)
			if p.ID == current {
				flags |= mfChecked
			}
			label := p.Label
			if label == "" {
				label = p.ID
			}
			appendMenu(menu, flags, uintptr(idProfileBase+i), "Switch to "+label)
		}
		appendMenu(menu, mfSeparator, 0, "")
	}
	appendMenu(menu, mfString, idQuit, "Quit Shunt")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmReturnCmd|tpmRightButton,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		uintptr(t.hwnd),
		0,
	)
	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)
	if cmd != 0 {
		handleCommand(t, uint32(cmd))
	}
}

func appendMenu(menu uintptr, flags, id uintptr, text string) {
	var ptr *uint16
	if text != "" {
		ptr, _ = syscall.UTF16PtrFromString(text)
	}
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(ptr)))
}

func handleCommand(t *trayInstance, id uint32) {
	switch {
	case id == idOpen:
		openDashboard(t)
	case id == idCopy:
		if t.opts.URL == nil {
			return
		}
		if err := copyText(t.hwnd, t.opts.URL()); err != nil {
			t.logger.Warn("tray clipboard copy failed", "error", err)
		}
	case id == idQuit:
		if t.opts.OnQuit != nil {
			t.opts.OnQuit()
		}
		procDestroyWindow.Call(uintptr(t.hwnd))
	case id >= idProfileBase:
		if t.opts.Profiles == nil || t.opts.OnSwitch == nil {
			return
		}
		profiles := t.opts.Profiles()
		index := int(id - idProfileBase)
		if index < 0 || index >= len(profiles) {
			return
		}
		if err := t.opts.OnSwitch(profiles[index].ID); err != nil {
			t.logger.Warn("tray switch failed", "profile", profiles[index].ID, "error", err)
			return
		}
		updateTooltip(t)
	}
}

func openDashboard(t *trayInstance) {
	if t.opts.OnOpen == nil {
		return
	}
	if err := t.opts.OnOpen(); err != nil {
		t.logger.Warn("tray open dashboard failed", "error", err)
	}
}

func copyText(hwnd syscall.Handle, text string) error {
	opened := false
	for i := 0; i < 10; i++ {
		ret, _, _ := procOpenClipboard.Call(uintptr(hwnd))
		if ret != 0 {
			opened = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	u16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(u16)*2))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock failed")
	}
	src := unsafe.Pointer(&u16[0])
	procRtlMoveMemory.Call(ptr, uintptr(src), uintptr(len(u16)*2))
	procGlobalUnlock.Call(h)
	if ret, _, _ := procSetClipboardData.Call(cfUnicodeText, h); ret == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData failed")
	}
	return nil
}
