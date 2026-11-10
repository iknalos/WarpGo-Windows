// Command warpgo is WarpGo for Windows: a free Cloudflare WARP client that
// tunnels the PC's traffic over WireGuard or MASQUE (HTTP/3, HTTP/2), with an
// Auto mode that tests which one really passes data on the current network.
//
//	WarpGo.exe                     open the window
//	WarpGo.exe --minimized         start in the tray
//	WarpGo.exe --connect           open the window and connect
//	WarpGo.exe --test              run every connection test, print results
//	WarpGo.exe --headless --connect=auto --for 30s --expect-warp
//
// Run `WarpGo.exe --help` from a terminal for every option.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/iknalos/WarpGo-Windows/internal/app"
	"github.com/iknalos/WarpGo-Windows/internal/engine"
	"github.com/iknalos/WarpGo-Windows/internal/ui"
	"github.com/iknalos/WarpGo-Windows/internal/wintun"
)

// Set by CI with -ldflags -X.
var (
	version = "0.0.0-dev"
	build   = "0"
)

func main() {
	attachConsole()
	os.Args = bareConnect(os.Args)

	minimized := flag.Bool("minimized", false, "start hidden in the tray")
	connect := flag.String("connect", "", "connect at start: auto, or one of "+modeIDs())
	headless := flag.Bool("headless", false, "no window: connect (needs --connect), print status, run until Ctrl+C or --for")
	test := flag.Bool("test", false, "run every connection test without connecting, print the results and exit")
	forDur := flag.Duration("for", 0, "headless: disconnect and exit after this long")
	routes := flag.String("routes", "", "headless: send only these comma-separated prefixes through the tunnel")
	expectWarp := flag.String("expect-warp", "", "headless: after connecting, fetch this Cloudflare trace URL and fail unless warp=on (e.g. https://1.0.0.1/cdn-cgi/trace)")
	wgFile := flag.String("wg-account", "", "use this WireGuard account (WarpGo JSON or a wgcf-profile.conf)")
	masqueFile := flag.String("masque-account", "", "use this MASQUE account (usque config.json)")
	logFile := flag.String("log", "", "also write the log to this file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	_ = flag.Bool("updated", false, "internal: started by the updater")
	_ = flag.Bool("relaunched", false, "internal: restarted as administrator")
	flag.Parse()

	if *showVersion {
		fmt.Printf("WarpGo for Windows %s (build %s)\n", version, build)
		return
	}
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			fatal(err)
		}
		os.Stderr = f
	}
	engine.EchoLogs(*headless || *test || *logFile != "")
	log.Printf("WarpGo for Windows %s (build %s)", version, build)

	elevated := windows.GetCurrentProcessToken().IsElevated()
	a := app.New(version, atoi(build), elevated)
	if *wgFile != "" || *masqueFile != "" {
		if err := a.UseAccountFiles(*wgFile, *masqueFile); err != nil {
			fatal(err)
		}
	}

	switch {
	case *test:
		os.Exit(runTests(a))
	case *headless:
		if *connect == "" {
			fatal(fmt.Errorf("--headless needs --connect"))
		}
		os.Exit(runHeadless(a, *connect, *routes, *forDur, *expectWarp))
	}

	// One window per PC: a second start just shows the first.
	mutex, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr("WarpGoWindowsApp"))
	if err == windows.ERROR_ALREADY_EXISTS {
		if !ui.ShowExisting() {
			ui.MessageBox("WarpGo is already running. Look for its icon in the tray.")
		}
		return
	}
	defer windows.CloseHandle(mutex)

	app.CleanupOldExe()
	if elevated {
		if err := wintun.Ensure(); err != nil {
			log.Printf("wintun.dll: %v", err)
		}
	}
	a.Prepare()
	a.CheckUpdate(false)
	go func() {
		for range time.Tick(6 * time.Hour) {
			a.CheckUpdate(false)
		}
	}()
	if *connect != "" && *connect != "auto" {
		a.SetMode(*connect)
	}
	ui.Run(a, ui.Options{
		Minimized: *minimized,
		Connect:   *connect != "" || a.Settings().ConnectOnLaunch,
	})
}

func runTests(a *app.App) int {
	a.TestAll()
	time.Sleep(200 * time.Millisecond)
	for a.State().Busy {
		time.Sleep(200 * time.Millisecond)
	}
	ok := 0
	for _, r := range a.State().Results {
		mark := "FAIL"
		if r.OK {
			mark = "ok  "
			ok++
		}
		fmt.Printf("%s %-24s %5d ms  %s\n", mark, r.Label, r.Millis, r.Detail)
	}
	fmt.Printf("%d of %d options pass data on this network\n", ok, len(engine.Modes))
	if ok == 0 {
		return 1
	}
	return 0
}

func runHeadless(a *app.App, mode, routes string, forDur time.Duration, expectWarp string) int {
	if !a.Elevated {
		fmt.Fprintln(os.Stderr, "WarpGo needs administrator rights to create its network adapter")
		return 2
	}
	if err := wintun.Ensure(); err != nil {
		fmt.Fprintln(os.Stderr, "wintun.dll:", err)
		return 2
	}
	var opts app.Options
	for _, r := range strings.Split(routes, ",") {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		p, err := netip.ParsePrefix(r)
		if err != nil {
			fatal(fmt.Errorf("bad route %q: %w", r, err))
		}
		opts.Engine.Routes = append(opts.Engine.Routes, p)
	}
	a.ConnectWith(mode, opts)
	defer a.Disconnect()

	// Wait for the handshake (WireGuard) or CONNECT-IP session (MASQUE), or
	// for data: with --routes nothing may flow until the trace below.
	deadline := time.Now().Add(90 * time.Second)
	for {
		s := a.State()
		if s.Stats.Running && (s.Stats.RX > 0 || s.Stats.Connected) {
			fmt.Printf("connected: %s (handshake=%v rx=%d bytes)\n", s.Stats.Mode, s.Stats.Connected, s.Stats.RX)
			break
		}
		if s.Phase == "error" {
			fmt.Fprintf(os.Stderr, "failed: %s: %s\n", s.Status, s.Detail)
			for _, r := range s.Results {
				fmt.Fprintf(os.Stderr, "  %-24s ok=%v %s\n", r.Label, r.OK, r.Detail)
			}
			return 1
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "timed out: %s: %s\n", s.Status, s.Detail)
			return 1
		}
		time.Sleep(250 * time.Millisecond)
	}

	if expectWarp != "" {
		var t *app.Trace
		var err error
		for i := 0; i < 5; i++ {
			if t, err = app.FetchTraceURL(expectWarp, 10*time.Second); err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "trace failed:", err)
			return 1
		}
		fmt.Printf("cloudflare trace: warp=%s ip=%s colo=%s loc=%s\n", t.Warp, t.IP, t.Colo, t.Loc)
		if t.Warp != "on" && t.Warp != "plus" {
			fmt.Fprintln(os.Stderr, "traffic is NOT going through WARP")
			return 1
		}
		if st := a.State().Stats; st.RX == 0 {
			fmt.Fprintln(os.Stderr, "the trace answered, but not through the tunnel (rx=0)")
			return 1
		}
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	var timeout <-chan time.Time
	if forDur > 0 {
		timeout = time.After(forDur)
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return 0
		case <-timeout:
			s := a.State().Stats
			fmt.Printf("done: %s rx=%d tx=%d after %ds\n", s.Mode, s.RX, s.TX, s.Seconds)
			return 0
		case <-tick.C:
			s := a.State().Stats
			if !s.Running {
				fmt.Fprintln(os.Stderr, "tunnel stopped unexpectedly")
				return 1
			}
			fmt.Printf("up %ds rx=%d tx=%d\n", s.Seconds, s.RX, s.TX)
		}
	}
}

func modeIDs() string {
	var ids []string
	for _, m := range engine.Modes {
		ids = append(ids, m.ID)
	}
	return strings.Join(ids, ", ")
}

// bareConnect turns a bare --connect (a string flag needs a value) into
// --connect=auto.
func bareConnect(args []string) []string {
	out := append([]string(nil), args[:1]...)
	for i := 1; i < len(args); i++ {
		a := args[i]
		if (a == "--connect" || a == "-connect") && (i+1 == len(args) || strings.HasPrefix(args[i+1], "-")) {
			a = "--connect=auto"
		}
		out = append(out, a)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "WarpGo:", err)
	os.Exit(2)
}

// attachConsole lets the GUI exe print to the terminal it was started from.
// When output is redirected (CI, pipes) the handles are already valid.
func attachConsole() {
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		return
	}
	const attachParentProcess = ^uint32(0) // ATTACH_PARENT_PROCESS
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := proc.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
