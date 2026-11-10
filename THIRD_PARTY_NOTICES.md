# Third-party software in WarpGo for Windows

| Component | Use | License |
|---|---|---|
| [wireguard-go](https://git.zx2c4.com/wireguard-go) | WireGuard tunnel | MIT |
| [wireguard-windows](https://git.zx2c4.com/wireguard-windows) (`tunnel/winipcfg`) | adapter addresses, routes, DNS | MIT |
| [Wintun](https://www.wintun.net) `wintun.dll` | virtual network adapter driver, shipped unmodified | Wintun Prebuilt Binaries License (see `wintun-LICENSE.txt`) |
| [usque](https://github.com/Diniboy1123/usque) | MASQUE (CONNECT-IP over HTTP/3 and HTTP/2) | MIT |
| [quic-go](https://github.com/quic-go/quic-go) | QUIC / HTTP/3 | MIT |
| [go-webview2](https://github.com/jchv/go-webview2) | window (Microsoft Edge WebView2) | MIT |
| [fyne systray](https://github.com/fyne-io/systray) | tray icon | Apache-2.0 |
| [golang.org/x/*](https://pkg.go.dev/golang.org/x) | crypto, networking, Windows APIs | BSD-3-Clause |

WireGuard is a registered trademark of Jason A. Donenfeld. Cloudflare and WARP
are trademarks of Cloudflare, Inc. WarpGo is an independent project and is not
affiliated with, endorsed by or sponsored by Cloudflare or WireGuard.
