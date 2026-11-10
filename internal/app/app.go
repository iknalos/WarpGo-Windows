// Package app is WarpGo's controller: accounts, Auto mode, tests, status and
// updates. The window (internal/ui) and the command line both drive it.
package app

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iknalos/WarpGo-Windows/internal/engine"
)

// ProbeLine is one test result.
type ProbeLine struct {
	Mode   string `json:"mode"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Millis int64  `json:"ms"`
	Detail string `json:"detail"`
}

// State is everything the window shows. It is polled, so it must be cheap.
type State struct {
	Phase    string        `json:"phase"` // idle, testing, connecting, connected, error
	Status   string        `json:"status"`
	Detail   string        `json:"detail"`
	Busy     bool          `json:"busy"`
	Stats    engine.Stats  `json:"stats"`
	Results  []ProbeLine   `json:"results"`
	Trace    *Trace        `json:"trace,omitempty"`
	Settings Settings      `json:"settings"`
	Account  string        `json:"account"`
	Modes    []engine.Mode `json:"modes"`
	Version  string        `json:"version"`
	Build    int           `json:"build"`
	Elevated bool          `json:"elevated"`
	Autorun  bool          `json:"autorun"`
	Update   *UpdateInfo   `json:"update,omitempty"`
	UpdMsg   string        `json:"updateMessage,omitempty"`
}

// App is the controller. One per process.
type App struct {
	Version  string
	Build    int
	Elevated bool

	st *store

	mu      sync.Mutex
	phase   string
	status  string
	detail  string
	busy    bool
	results []ProbeLine
	trace   *Trace
	gen     int // bumps on every connect/disconnect, so stale workers stop

	upd    *UpdateInfo
	updMsg string

	autorun atomic.Bool // cached: asking Task Scheduler on every poll is slow

	// OnChange is called (from any goroutine) when the connection state changes,
	// so the tray can follow.
	OnChange func(connected bool, tooltip string)
}

// New loads settings and accounts from %AppData%\WarpGo.
func New(version string, build int, elevated bool) *App {
	a := &App{Version: version, Build: build, Elevated: elevated, st: openStore(DataDir()),
		phase: "idle", status: "Not connected"}
	a.autorun.Store(autorunEnabled())
	return a
}

// UseAccountFiles uses WARP accounts from files for this run (not saved).
func (a *App) UseAccountFiles(wg, masque string) error { return a.st.useFiles(wg, masque) }

// Settings returns the current settings.
func (a *App) Settings() Settings { return a.st.settings() }

// State snapshots everything for the window.
func (a *App) State() State {
	a.mu.Lock()
	s := State{
		Phase: a.phase, Status: a.status, Detail: a.detail, Busy: a.busy,
		Results: append([]ProbeLine(nil), a.results...), Trace: a.trace,
		Update: a.upd, UpdMsg: a.updMsg,
	}
	a.mu.Unlock()
	s.Stats = engine.CurrentStats()
	s.Settings = a.st.settings()
	s.Account = a.st.accountSource()
	s.Modes = engine.Modes
	s.Version, s.Build, s.Elevated = a.Version, a.Build, a.Elevated
	s.Autorun = a.autorun.Load()
	return s
}

// StateJSON is State as JSON (for the command line).
func (a *App) StateJSON() string {
	b, _ := json.Marshal(a.State())
	return string(b)
}

func (a *App) set(phase, status, detail string) {
	a.mu.Lock()
	a.phase, a.status, a.detail = phase, status, detail
	a.mu.Unlock()
	if phase != "testing" {
		log.Printf("status: %s. %s", status, detail)
	}
	a.notify()
}

func (a *App) notify() {
	if a.OnChange == nil {
		return
	}
	st := engine.CurrentStats()
	a.mu.Lock()
	status := a.status
	a.mu.Unlock()
	tip := "WarpGo: " + status
	if st.Running {
		if m, ok := engine.ModeByID(st.Mode); ok {
			tip = "WarpGo: " + status + " (" + m.Label + ")"
		}
	}
	a.OnChange(st.Running, tip)
}

// SetMode picks Auto or one fixed mode for the next connect.
func (a *App) SetMode(mode string) {
	if _, ok := engine.ModeByID(mode); !ok {
		mode = "auto"
	}
	a.st.update(func(s *Settings) { s.Mode = mode })
}

// SetOption changes a yes/no setting.
func (a *App) SetOption(name string, on bool) error {
	switch name {
	case "connectOnLaunch":
		a.st.update(func(s *Settings) { s.ConnectOnLaunch = on })
	case "closeToTray":
		a.st.update(func(s *Settings) { s.CloseToTray = on })
	case "autorun":
		err := setAutorun(on)
		a.autorun.Store(autorunEnabled())
		return err
	default:
		return fmt.Errorf("unknown setting %q", name)
	}
	return nil
}

// Connect starts connecting in the background with the saved mode.
func (a *App) Connect() {
	a.ConnectWith(a.st.settings().Mode, Options{})
}

// Options are command-line overrides for a connection.
type Options struct {
	Engine engine.Options
}

// ConnectWith starts connecting in the background. mode is "auto" or a mode ID.
func (a *App) ConnectWith(mode string, opts Options) {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return
	}
	a.busy = true
	a.gen++
	gen := a.gen
	a.results = nil
	a.trace = nil
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.busy = false
			a.mu.Unlock()
			a.notify()
		}()
		engine.Stop()
		if !a.Elevated {
			a.set("error", "Needs administrator rights", "Windows only lets administrator apps create a VPN adapter. Start WarpGo with Run as administrator.")
			return
		}
		var err error
		if m, ok := engine.ModeByID(mode); ok {
			a.set("connecting", "Connecting…", m.Label)
			err = a.start(m, opts)
		} else {
			err = a.auto(gen, opts)
		}
		if err != nil {
			if a.current(gen) {
				a.set("error", "Not connected", err.Error())
			}
			return
		}
		go a.watch(gen)
	}()
}

func (a *App) current(gen int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen == gen
}

func (a *App) start(m engine.Mode, opts Options) error {
	acct, err := a.st.account(m)
	if err != nil {
		return err
	}
	if err := engine.Start(m, acct, opts.Engine); err != nil {
		return err
	}
	a.set("connecting", "Connected, checking data…", m.Label)
	return nil
}

// auto tests each mode in turn and connects with the first one whose test
// gets a DNS answer back through the tunnel.
func (a *App) auto(gen int, opts Options) error {
	network := engine.NetworkKey()
	order := engine.AutoOrder(a.st.remembered(network))
	for _, m := range order {
		if !a.current(gen) {
			return nil
		}
		a.set("connecting", "Testing "+m.Label+"…", "Auto tries each way of reaching Cloudflare and uses the first that really passes data.")
		r := a.probe(m)
		a.addResult(r)
		if !r.OK {
			continue
		}
		if !a.current(gen) {
			return nil
		}
		if err := a.start(m, opts); err != nil {
			log.Printf("auto: %s passed its test but the tunnel failed: %v", m.ID, err)
			a.addResult(ProbeLine{Mode: m.ID, Label: m.Label, Detail: "test passed, tunnel failed: " + err.Error()})
			continue
		}
		a.st.remember(network, m.ID)
		return nil
	}
	return fmt.Errorf("nothing got through on this network. Results are below; try again on another network or send the log")
}

func (a *App) addResult(r ProbeLine) {
	a.mu.Lock()
	a.results = append(a.results, r)
	a.mu.Unlock()
}

func (a *App) probe(m engine.Mode) ProbeLine {
	line := ProbeLine{Mode: m.ID, Label: m.Label}
	acct, err := a.st.account(m)
	if err != nil {
		line.Detail = err.Error()
		return line
	}
	var raw string
	if m.IsMasque() {
		raw = engine.ProbeMasque(acct, m.HTTP2, 6000)
	} else {
		raw = engine.ProbeWireGuard(acct, m.WgPort, 5000)
	}
	var r struct {
		OK     bool   `json:"ok"`
		Millis int64  `json:"ms"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		line.Detail = raw
		return line
	}
	line.OK, line.Millis, line.Detail = r.OK, r.Millis, r.Detail
	return line
}

// watch follows a fresh tunnel: Connected ✓ only once data comes back, then
// asks Cloudflare whether the traffic really arrives over WARP.
func (a *App) watch(gen int) {
	deadline := time.Now().Add(12 * time.Second)
	warned := false
	traced := false
	for a.current(gen) {
		st := engine.CurrentStats()
		if !st.Running {
			return
		}
		m, _ := engine.ModeByID(st.Mode)
		switch {
		case st.RX > 0:
			a.mu.Lock()
			wasConnected := a.phase == "connected"
			a.mu.Unlock()
			if !wasConnected {
				a.set("connected", "Connected ✓", m.Label)
			}
			if !traced {
				traced = true
				go func() {
					t, err := FetchTrace(8 * time.Second)
					if err != nil {
						log.Printf("trace: %v", err)
						return
					}
					if a.current(gen) {
						a.mu.Lock()
						a.trace = t
						a.mu.Unlock()
						log.Printf("cloudflare trace: warp=%s ip=%s colo=%s", t.Warp, t.IP, t.Colo)
					}
				}()
			}
		case !warned && time.Now().After(deadline):
			warned = true
			a.set("connecting", "Connected, but no data yet", m.Label+": nothing has come back through the tunnel in 12 seconds. This network may block it; Disconnect and use Auto.")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Disconnect tears the tunnel down.
func (a *App) Disconnect() {
	a.mu.Lock()
	a.gen++
	a.trace = nil
	a.mu.Unlock()
	engine.Stop()
	a.set("idle", "Not connected", "")
}

// TestAll runs every test without connecting and lists the results.
func (a *App) TestAll() {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return
	}
	a.busy = true
	a.gen++
	gen := a.gen
	a.results = nil
	a.trace = nil
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			a.busy = false
			a.mu.Unlock()
		}()
		engine.Stop()
		ok := 0
		for _, m := range engine.Modes {
			if !a.current(gen) {
				return
			}
			a.set("testing", "Testing "+m.Label+"…", "Nothing is connected while the tests run.")
			r := a.probe(m)
			if r.OK {
				ok++
			}
			a.addResult(r)
		}
		a.set("idle", "Not connected", fmt.Sprintf("%d of %d options pass data on this network.", ok, len(engine.Modes)))
	}()
}

// ResetAccounts forgets the WARP accounts; the next connect registers new ones.
func (a *App) ResetAccounts() {
	a.Disconnect()
	a.st.resetAccounts()
	a.set("idle", "Not connected", "WARP accounts cleared. New ones are created on the next connect.")
}

// Prepare registers accounts in the background at start-up so the first
// connect doesn't wait for Cloudflare's signup API.
func (a *App) Prepare() {
	go func() {
		if _, err := a.st.wgAccount(); err != nil {
			log.Printf("prepare: %v", err)
		}
		if _, err := a.st.masqueAccount(); err != nil {
			log.Printf("prepare: %v", err)
		}
	}()
}

// Logs returns the recent engine log.
func (a *App) Logs() string {
	return strings.TrimSpace(engine.Logs())
}
