// Command register creates fresh free WARP accounts (one WireGuard, one
// MASQUE) and writes them as WarpGo account JSON, e.g. for the built-in
// fallback accounts that CI bakes into release builds.
//
//	go run ./tools/register -wg wg.json -masque masque.json
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/iknalos/WarpGo-Windows/internal/engine"
)

func main() {
	wg := flag.String("wg", "", "write a new WireGuard account here")
	masque := flag.String("masque", "", "write a new MASQUE account here")
	flag.Parse()
	if *wg == "" && *masque == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *wg != "" {
		acct, err := engine.RegisterWireGuard()
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*wg, []byte(acct), 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Println("WireGuard account written to", *wg)
	}
	if *masque != "" {
		acct, err := engine.RegisterMasque()
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*masque, []byte(acct), 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Println("MASQUE account written to", *masque)
	}
}
