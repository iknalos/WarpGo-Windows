package engine

// Mode is one way of reaching Cloudflare WARP. The IDs match the Android app's,
// so both report the same names.
type Mode struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Hint   string `json:"hint"`
	WgPort int    `json:"wgPort,omitempty"`
	HTTP2  bool   `json:"http2,omitempty"`
}

// IsMasque reports whether the mode is a MASQUE (HTTP/3 or HTTP/2) tunnel.
func (m Mode) IsMasque() bool { return m.WgPort == 0 }

// Modes lists every option, in the order Auto tries them when it has nothing
// remembered for the network: WireGuard 4500 first (what WarpGo has always
// used), then the other WireGuard ports, then MASQUE.
var Modes = []Mode{
	{ID: "wireguard-4500", Label: "WireGuard · port 4500", Hint: "fastest, works on most networks", WgPort: 4500},
	{ID: "wireguard-2408", Label: "WireGuard · port 2408", Hint: "the WARP default port", WgPort: 2408},
	{ID: "wireguard-500", Label: "WireGuard · port 500", Hint: "looks like IPsec VPN traffic", WgPort: 500},
	{ID: "masque-h3", Label: "MASQUE · HTTP/3", Hint: "looks like web traffic (UDP 443)"},
	{ID: "masque-h2", Label: "MASQUE · HTTP/2", Hint: "for networks that block UDP (TCP 443)", HTTP2: true},
}

// ModeByID finds a mode by its ID.
func ModeByID(id string) (Mode, bool) {
	for _, m := range Modes {
		if m.ID == id {
			return m, true
		}
	}
	return Mode{}, false
}

// AutoOrder is the order Auto tests the modes in: whatever worked last time on
// this network first, then the default order.
func AutoOrder(remembered string) []Mode {
	out := make([]Mode, 0, len(Modes))
	if m, ok := ModeByID(remembered); ok {
		out = append(out, m)
	}
	for _, m := range Modes {
		if m.ID != remembered {
			out = append(out, m)
		}
	}
	return out
}
