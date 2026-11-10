package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/iknalos/WarpGo-Windows/internal/engine"
)

// Pre-registered WARP accounts, injected at build time from GitHub Actions
// secrets (base64 JSON), for networks that intercept WARP's signup API.
// Empty in local builds, which then only use accounts registered on the PC.
var (
	bakedWg     string
	bakedMasque string
)

// Settings are saved in %AppData%\WarpGo\settings.json.
type Settings struct {
	Mode            string            `json:"mode"`            // "auto" or a mode ID
	ConnectOnLaunch bool              `json:"connectOnLaunch"` // connect when WarpGo starts
	CloseToTray     bool              `json:"closeToTray"`     // closing the window keeps the tunnel up
	Memory          map[string]string `json:"memory"`          // network key -> mode ID that worked there
}

// Accounts are saved in %AppData%\WarpGo\accounts.json. They hold private
// keys, so the file is only readable by the user.
type Accounts struct {
	WG           string `json:"wg,omitempty"`
	WGSource     string `json:"wg_source,omitempty"` // "registered" or "built-in"
	Masque       string `json:"masque,omitempty"`
	MasqueSource string `json:"masque_source,omitempty"`
}

type store struct {
	dir   string
	regMu sync.Mutex // one signup at a time (start-up prepare vs. a click on Connect)
	mu    sync.Mutex
	s     Settings
	a     Accounts
}

// DataDir is where WarpGo keeps its files.
func DataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "WarpGo")
}

func openStore(dir string) *store {
	st := &store{dir: dir, s: Settings{Mode: "auto", CloseToTray: true}}
	_ = os.MkdirAll(dir, 0o700)
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &st.s)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "accounts.json")); err == nil {
		_ = json.Unmarshal(b, &st.a)
	}
	if st.s.Mode == "" {
		st.s.Mode = "auto"
	}
	if st.s.Memory == nil {
		st.s.Memory = map[string]string{}
	}
	return st
}

func (st *store) settings() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	c := st.s
	c.Memory = nil
	return c
}

func (st *store) update(f func(*Settings)) {
	st.mu.Lock()
	f(&st.s)
	b, _ := json.MarshalIndent(st.s, "", "  ")
	st.mu.Unlock()
	writeFile(filepath.Join(st.dir, "settings.json"), b)
}

func (st *store) remembered(network string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.Memory[network]
}

func (st *store) remember(network, mode string) {
	if network == "" {
		return
	}
	st.update(func(s *Settings) { s.Memory[network] = mode })
}

func (st *store) saveAccounts() {
	st.mu.Lock()
	b, _ := json.MarshalIndent(st.a, "", "  ")
	st.mu.Unlock()
	writeFile(filepath.Join(st.dir, "accounts.json"), b)
}

func (st *store) resetAccounts() {
	st.mu.Lock()
	st.a = Accounts{}
	st.mu.Unlock()
	_ = os.Remove(filepath.Join(st.dir, "accounts.json"))
}

func (st *store) accountSource() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case st.a.WGSource == "" && st.a.MasqueSource == "":
		return ""
	case st.a.WGSource == "built-in" || st.a.MasqueSource == "built-in":
		return "built-in"
	case st.a.WGSource == "file" || st.a.MasqueSource == "file":
		return "file"
	default:
		return "registered"
	}
}

// wgAccount returns the WireGuard account: the saved one, else a fresh
// registration on this PC, else the built-in one.
func (st *store) wgAccount() (string, error) {
	st.regMu.Lock()
	defer st.regMu.Unlock()
	st.mu.Lock()
	if st.a.WG != "" {
		defer st.mu.Unlock()
		return st.a.WG, nil
	}
	st.mu.Unlock()
	acct, src, err := obtain("WireGuard", engine.RegisterWireGuard, bakedWg)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	st.a.WG, st.a.WGSource = acct, src
	st.mu.Unlock()
	st.saveAccounts()
	return acct, nil
}

// masqueAccount is wgAccount for MASQUE.
func (st *store) masqueAccount() (string, error) {
	st.regMu.Lock()
	defer st.regMu.Unlock()
	st.mu.Lock()
	if st.a.Masque != "" {
		defer st.mu.Unlock()
		return st.a.Masque, nil
	}
	st.mu.Unlock()
	acct, src, err := obtain("MASQUE", engine.RegisterMasque, bakedMasque)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	st.a.Masque, st.a.MasqueSource = acct, src
	st.mu.Unlock()
	st.saveAccounts()
	return acct, nil
}

func (st *store) account(m engine.Mode) (string, error) {
	if m.IsMasque() {
		return st.masqueAccount()
	}
	return st.wgAccount()
}

func obtain(kind string, register func() (string, error), baked string) (string, string, error) {
	start := time.Now()
	acct, err := register()
	if err == nil {
		log.Printf("registered a new %s account on this PC (%s)", kind, time.Since(start).Round(time.Millisecond))
		return acct, "registered", nil
	}
	log.Printf("%s signup failed: %v", kind, err)
	if baked != "" {
		if raw, derr := base64.StdEncoding.DecodeString(baked); derr == nil {
			log.Printf("using the built-in %s account", kind)
			return string(raw), "built-in", nil
		}
	}
	return "", "", errors.New("could not create a WARP account: " + err.Error())
}

// useFiles loads accounts from files for this run only (command line).
func (st *store) useFiles(wgPath, masquePath string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if wgPath != "" {
		b, err := os.ReadFile(wgPath)
		if err != nil {
			return err
		}
		acct := string(b)
		if !strings.HasPrefix(strings.TrimSpace(acct), "{") {
			if acct, err = wgFromConf(acct); err != nil {
				return fmt.Errorf("%s: %w", wgPath, err)
			}
		}
		st.a.WG, st.a.WGSource = acct, "file"
	}
	if masquePath != "" {
		b, err := os.ReadFile(masquePath)
		if err != nil {
			return err
		}
		st.a.Masque, st.a.MasqueSource = string(b), "file"
	}
	return nil
}

// wgFromConf reads PrivateKey and Address from a wgcf-profile.conf.
func wgFromConf(conf string) (string, error) {
	var priv, v4, v6 string
	for _, line := range strings.Split(conf, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "PrivateKey":
			priv = strings.TrimSpace(v)
		case "Address":
			for _, a := range strings.Split(v, ",") {
				if a = strings.TrimSpace(a); strings.Contains(a, ":") {
					v6 = a
				} else if a != "" {
					v4 = a
				}
			}
		}
	}
	if priv == "" {
		return "", errors.New("no PrivateKey")
	}
	b, _ := json.Marshal(map[string]string{"private_key": priv, "address_v4": v4, "address_v6": v6})
	return string(b), nil
}

func writeFile(path string, data []byte) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("save %s: %v", filepath.Base(path), err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("save %s: %v", filepath.Base(path), err)
	}
}
