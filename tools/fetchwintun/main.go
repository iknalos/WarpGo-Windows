// Command fetchwintun downloads the official signed Wintun driver DLL
// (https://www.wintun.net), checks the archive's published SHA-256 and puts
// wintun.dll and its license in internal/wintun for embedding.
//
//	go run ./tools/fetchwintun [-arch amd64]
package main

import (
	"archive/zip"
	"bytes"
	sha "crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

const (
	url    = "https://www.wintun.net/builds/wintun-0.14.1.zip"
	sha256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
)

func main() {
	arch := flag.String("arch", "amd64", "amd64, arm64, x86 or arm")
	out := flag.String("out", filepath.Join("internal", "wintun"), "output directory")
	flag.Parse()

	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	sum := sha256Hex(data)
	if sum != sha256 {
		log.Fatalf("wintun zip SHA-256 is %s, want %s", sum, sha256)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Fatal(err)
	}
	want := map[string]string{
		"wintun/bin/" + *arch + "/wintun.dll": "wintun.dll",
		"wintun/LICENSE.txt":                  "LICENSE.txt",
	}
	for _, f := range zr.File {
		name, ok := want[f.Name]
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			log.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s -> %s (%d bytes)\n", f.Name, filepath.Join(*out, name), len(b))
		delete(want, f.Name)
	}
	if len(want) > 0 {
		log.Fatalf("missing from the archive: %v", want)
	}
}

func sha256Hex(b []byte) string {
	h := sha.Sum256(b)
	return hex.EncodeToString(h[:])
}
