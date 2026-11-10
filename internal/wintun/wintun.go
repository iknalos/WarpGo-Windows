// Package wintun ships the signed Wintun driver DLL (https://www.wintun.net,
// WireGuard LLC) inside WarpGo.exe, so the portable exe works on its own.
// wintun.dll is fetched by `go run ./tools/fetchwintun` and is not in git.
package wintun

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed wintun.dll
var dll []byte

// Ensure puts wintun.dll next to the running exe, where wireguard-go loads
// it from, unless an identical copy is already there.
func Ensure() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, dll) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, dll, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
