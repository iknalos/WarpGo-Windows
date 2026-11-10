package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/iknalos/WarpGo-Windows/internal/engine"
)

// Repo is where releases are published.
const Repo = "iknalos/WarpGo-Windows"

const (
	latestURL      = "https://github.com/" + Repo + "/releases/latest/download/latest.json"
	downloadPrefix = "https://github.com/" + Repo + "/releases/download/"
)

// UpdateInfo is the latest.json CI publishes with every release.
type UpdateInfo struct {
	Build          int    `json:"build"`
	Version        string `json:"version"`
	Setup          string `json:"setup"`
	SetupSHA256    string `json:"setup_sha256"`
	Portable       string `json:"portable"`
	PortableSHA256 string `json:"portable_sha256"`
	Notes          string `json:"notes"`
}

// CheckUpdate looks for a newer release in the background.
func (a *App) CheckUpdate(manual bool) {
	go func() {
		if manual {
			a.setUpd(nil, "Checking for updates…")
		}
		info, err := fetchLatest()
		switch {
		case err != nil:
			log.Printf("update check: %v", err)
			if manual {
				a.setUpd(nil, "Could not check for updates: "+err.Error())
			}
		case info == nil || info.Build <= a.Build:
			if manual {
				a.setUpd(nil, "WarpGo is up to date.")
			}
		default:
			log.Printf("update available: %s (build %d)", info.Version, info.Build)
			a.setUpd(info, "")
		}
	}()
}

func (a *App) setUpd(info *UpdateInfo, msg string) {
	a.mu.Lock()
	a.upd, a.updMsg = info, msg
	a.mu.Unlock()
}

// DismissUpdate hides the update banner until the next check.
func (a *App) DismissUpdate() { a.setUpd(nil, "") }

func fetchLatest() (*UpdateInfo, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(latestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var info UpdateInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Installed reports whether this copy was installed by WarpGo-Setup.exe
// (the uninstaller sits next to it) rather than run as the portable exe.
func Installed() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(exe), "unins000.exe"))
	return err == nil
}

// InstallUpdate downloads the new build, checks its SHA-256 and swaps it in.
// quit is called once the new version has been started.
func (a *App) InstallUpdate(quit func()) {
	a.mu.Lock()
	info := a.upd
	a.mu.Unlock()
	if info == nil {
		return
	}
	go func() {
		a.setUpd(info, "Downloading "+info.Version+"…")
		if err := a.applyUpdate(info, quit); err != nil {
			log.Printf("update: %v", err)
			a.setUpd(info, "Update failed: "+err.Error())
		}
	}()
}

func (a *App) applyUpdate(info *UpdateInfo, quit func()) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if Installed() {
		setup := filepath.Join(os.TempDir(), "WarpGo-Setup-"+fmt.Sprint(info.Build)+".exe")
		if err := download(info.Setup, info.SetupSHA256, setup); err != nil {
			return err
		}
		engine.Stop()
		// The installer closes nothing itself; we exit right after starting it,
		// and it starts the new WarpGo when it is done.
		cmd := exec.Command(setup, "/SILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/NOCANCEL")
		if err := cmd.Start(); err != nil {
			return err
		}
		quit()
		return nil
	}
	// Portable: a running exe can be renamed but not overwritten.
	next := exe + ".new"
	if err := download(info.Portable, info.PortableSHA256, next); err != nil {
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	engine.Stop()
	cmd := exec.Command(exe, "--updated")
	if err := cmd.Start(); err != nil {
		return err
	}
	quit()
	return nil
}

// CleanupOldExe removes the copy a portable update left behind.
func CleanupOldExe() {
	if exe, err := os.Executable(); err == nil {
		for i := 0; i < 10; i++ {
			if err := os.Remove(exe + ".old"); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(300 * time.Millisecond) // the old process may still be exiting
		}
	}
}

func download(url, sum, dest string) error {
	if !strings.HasPrefix(url, downloadPrefix) {
		return errors.New("refusing a download from outside " + Repo)
	}
	if len(sum) != 64 {
		return errors.New("latest.json has no SHA-256")
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		_ = os.Remove(dest)
		return errors.New("download is corrupt (SHA-256 mismatch)")
	}
	return nil
}
