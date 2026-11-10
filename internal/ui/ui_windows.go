//go:build windows

// Package ui is WarpGo's window (WebView2) and tray icon.
package ui

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"unsafe"

	"fyne.io/systray"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/iknalos/WarpGo-Windows/internal/app"
)

//go:embed index.html
var indexHTML string

//go:embed tray-off.ico
var iconICO []byte

//go:embed tray-on.ico
var iconOnICO []byte

// Title is the window title; a second WarpGo finds the first by it.
const Title = "WarpGo"

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW      = user32.NewProc("FindWindowW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForegroundWnd = user32.NewProc("SetForegroundWindow")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
)

const (
	swHide    = 0
	swShow    = 5
	swRestore = 9
	wmClose   = 0x0010
)

// Options for the window.
type Options struct {
	Minimized bool // start hidden in the tray
	Connect   bool // connect right away
}

// ShowExisting brings an already running WarpGo to the front. It reports
// whether one was found.
func ShowExisting() bool {
	title, _ := windows.UTF16PtrFromString(Title)
	class, _ := windows.UTF16PtrFromString("webview")
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)))
	if h == 0 {
		return false
	}
	show(h)
	return true
}

func show(h uintptr) {
	procShowWindow.Call(h, swShow)
	procShowWindow.Call(h, swRestore)
	procSetForegroundWnd.Call(h)
}

// MessageBox shows a plain Windows message box.
func MessageBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(Title)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x40) // MB_ICONINFORMATION
}

// Run shows the window and tray icon and returns when the user quits.
func Run(a *app.App, opts Options) {
	runtime.LockOSThread()

	scale := 1.0
	if dpi, _, _ := procGetDpiForSystem.Call(); dpi > 0 {
		scale = float64(dpi) / 96
	}
	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "WarpGo", "WebView2")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  dataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  Title,
			Width:  uint(470 * scale),
			Height: uint(860 * scale),
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		MessageBox("WarpGo needs the Microsoft Edge WebView2 Runtime, which is part of Windows 11 and most Windows 10 PCs.\n\nInstall it from https://go.microsoft.com/fwlink/p/?LinkId=2124703 and start WarpGo again.")
		return
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	w.SetSize(int(380*scale), int(560*scale), webview2.HintMin)
	if opts.Minimized {
		procShowWindow.Call(hwnd, swHide)
	}

	var quitting atomic.Bool
	quit := func() {
		if quitting.Swap(true) {
			return
		}
		go func() {
			a.Disconnect()
			systray.Quit() // destroys the tray window, which ends the message loop
		}()
	}

	// Closing the window hides it to the tray while that setting is on.
	var orig uintptr
	proc := windows.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
		if msg == wmClose {
			if !quitting.Load() && a.Settings().CloseToTray {
				procShowWindow.Call(h, swHide)
				return 0
			}
			quit()
			return 0
		}
		r, _, _ := procCallWindowProc.Call(orig, h, msg, wp, lp)
		return r
	})
	gwlpWndProc := ^uintptr(3) // -4
	orig, _, _ = procSetWindowLongPtr.Call(hwnd, gwlpWndProc, proc)

	bind(w, a, quit)
	w.SetHtml(indexHTML)

	// Tray icon. Registered on this thread, so the window's message loop also
	// serves the tray's hidden window.
	var tray trayItems
	systray.Register(func() { tray.build(a, hwnd, quit) }, nil)
	a.OnChange = func(connected bool, tip string) { tray.update(connected, tip) }

	if opts.Connect {
		a.Connect()
	}
	w.Run()
	a.Disconnect()
}

func bind(w webview2.WebView, a *app.App, quit func()) {
	must := func(err error) {
		if err != nil {
			log.Printf("bind: %v", err)
		}
	}
	must(w.Bind("state", func() app.State { return a.State() }))
	must(w.Bind("connect", func() { a.Connect() }))
	must(w.Bind("disconnect", func() { go a.Disconnect() }))
	must(w.Bind("setMode", func(m string) { a.SetMode(m) }))
	must(w.Bind("testAll", func() { a.TestAll() }))
	must(w.Bind("resetAccount", func() { go a.ResetAccounts() }))
	must(w.Bind("setOption", func(name string, on bool) error { return a.SetOption(name, on) }))
	must(w.Bind("checkUpdate", func() { a.CheckUpdate(true) }))
	must(w.Bind("installUpdate", func() { a.InstallUpdate(quit) }))
	must(w.Bind("dismissUpdate", func() { a.DismissUpdate() }))
	must(w.Bind("logs", func() string { return a.Logs() }))
	must(w.Bind("openURL", func(u string) {
		if strings.HasPrefix(u, "https://github.com/iknalos/") || strings.HasPrefix(u, "https://iknalos.github.io/") {
			openURL(u)
		}
	}))
	must(w.Bind("relaunchAdmin", func() {
		if err := RelaunchElevated(); err == nil {
			quit()
		}
	}))
}

func openURL(u string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(u)
	_ = windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

// RelaunchElevated starts this exe again with "Run as administrator".
func RelaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--relaunched")
	return windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL)
}

type trayItems struct {
	ready  atomic.Bool
	toggle *systray.MenuItem
}

func (t *trayItems) build(a *app.App, hwnd uintptr, quit func()) {
	systray.SetIcon(iconICO)
	systray.SetTooltip("WarpGo: Not connected")
	open := systray.AddMenuItem("Open WarpGo", "Show the WarpGo window")
	systray.AddSeparator()
	t.toggle = systray.AddMenuItem("Connect", "Connect or disconnect")
	systray.AddSeparator()
	exit := systray.AddMenuItem("Quit WarpGo", "Disconnect and quit")
	systray.SetOnTapped(func() { show(hwnd) })
	t.ready.Store(true)
	go func() {
		for {
			select {
			case <-open.ClickedCh:
				show(hwnd)
			case <-t.toggle.ClickedCh:
				if a.State().Stats.Running || a.State().Busy {
					a.Disconnect()
				} else {
					a.Connect()
				}
			case <-exit.ClickedCh:
				quit()
				return
			}
		}
	}()
}

var lastConnected atomic.Int32 // 0 unknown, 1 off, 2 on

func (t *trayItems) update(connected bool, tip string) {
	if !t.ready.Load() {
		return
	}
	systray.SetTooltip(tip)
	want := int32(1)
	if connected {
		want = 2
	}
	if lastConnected.Swap(want) == want {
		return
	}
	if connected {
		systray.SetIcon(iconOnICO)
		t.toggle.SetTitle("Disconnect")
	} else {
		systray.SetIcon(iconICO)
		t.toggle.SetTitle("Connect")
	}
}
