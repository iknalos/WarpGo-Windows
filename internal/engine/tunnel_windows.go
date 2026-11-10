//go:build windows

package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Diniboy1123/usque/api"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// AdapterName is the name of the Wintun network adapter WarpGo creates.
const AdapterName = "WarpGo"

// A fixed GUID, so Windows sees the same network adapter on every connect
// instead of piling up "WarpGo 2", "WarpGo 3"...
var adapterGUID = windows.GUID{
	Data1: 0x57415250, Data2: 0x474f, Data3: 0x5749,
	Data4: [8]byte{0x4e, 0x44, 0x4f, 0x57, 0x53, 0x00, 0x00, 0x01},
}

var (
	dnsV4 = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1")}
	dnsV6 = []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("2606:4700:4700::1001")}

	// Two halves of the address space instead of 0.0.0.0/0: they beat the
	// existing default route without touching it, and disappear with the
	// adapter, so a crash can't leave the PC without internet.
	splitV4 = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
	splitV6 = []netip.Prefix{netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1")}
)

// Options tune a tunnel. The zero value is a normal full tunnel.
type Options struct {
	// Routes, if set, sends only these prefixes through the tunnel instead of
	// everything (used by the CI test so the runner keeps its own connection).
	Routes []netip.Prefix
}

type session struct {
	mode    Mode
	tun     *countTun
	wg      *device.Device     // WireGuard sessions
	cancel  context.CancelFunc // MASQUE sessions
	done    chan struct{}      // closed when the MASQUE loop returns
	bypass  *bypassRoute
	started time.Time
}

var (
	sessMu sync.Mutex
	sess   *session
)

// Start brings up a tunnel in the given mode on a Wintun adapter and routes
// the PC's traffic through it. Needs administrator rights.
func Start(mode Mode, accountJSON string, opts Options) error {
	Stop()

	var (
		tunAddrs []netip.Prefix
		endpoint netip.AddrPort
		wgAcct   *WgAccount
		mCfg     api.MaintainTunnelConfig
	)
	if mode.IsMasque() {
		acct, err := parseMasque(accountJSON)
		if err != nil {
			return err
		}
		if mCfg, err = masqueConfig(acct, mode.HTTP2); err != nil {
			return err
		}
		ep := acct.EndpointV4
		if mode.HTTP2 {
			ep = acct.EndpointH2V4
		}
		ip, err := netip.ParseAddr(ep)
		if err != nil {
			return fmt.Errorf("bad MASQUE endpoint %q", ep)
		}
		endpoint = netip.AddrPortFrom(ip, 443)
		tunAddrs = hostPrefixes(acct.IPv4, acct.IPv6)
	} else {
		acct, err := parseWg(accountJSON)
		if err != nil {
			return err
		}
		wgAcct = acct
		ap, err := netip.ParseAddrPort(wgEndpoint(mode.WgPort))
		if err != nil {
			return err
		}
		endpoint = ap
		tunAddrs = hostPrefixes(acct.AddressV4, acct.AddressV6)
	}
	if len(tunAddrs) == 0 {
		return errors.New("the WARP account has no tunnel address")
	}

	// Keep the tunnel's own packets to Cloudflare on the real network.
	bypass, err := newBypassRoute(endpoint.Addr())
	if err != nil {
		return err
	}

	nt, err := tun.CreateTUNWithRequestedGUID(AdapterName, &adapterGUID, tunnelMTU)
	if err != nil {
		bypass.remove()
		return fmt.Errorf("could not create the WarpGo network adapter (run as administrator; wintun.dll must be next to WarpGo.exe): %w", err)
	}
	native, ok := nt.(*tun.NativeTun)
	if !ok {
		_ = nt.Close()
		bypass.remove()
		return errors.New("unexpected adapter type")
	}
	luid := winipcfg.LUID(native.LUID())
	bypass.ourLUID = luid

	routes := opts.Routes
	if len(routes) == 0 {
		routes = splitV4
		if hasV6(tunAddrs) {
			routes = append(append([]netip.Prefix(nil), splitV4...), splitV6...)
		}
	}
	if err := configureAdapter(luid, tunAddrs, routes); err != nil {
		_ = nt.Close()
		bypass.remove()
		return err
	}

	t := newCountTun(nt)
	s := &session{mode: mode, tun: t, bypass: bypass, started: time.Now()}
	if mode.IsMasque() {
		mCfg.Device = t
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.done = make(chan struct{})
		masqueUp.Store(false)
		go func() {
			api.MaintainTunnel(ctx, mCfg)
			close(s.done)
		}()
	} else {
		dev, err := newWgDeviceAt(t, wgAcct, endpoint.String(), "tunnel")
		if err != nil {
			bypass.remove()
			return err // newWgDeviceAt closed the adapter
		}
		s.wg = dev
	}
	bypass.watch()

	sessMu.Lock()
	sess = s
	sessMu.Unlock()
	log.Printf("tunnel started: %s via %s", mode.ID, endpoint)
	return nil
}

// Stop tears down whichever tunnel is running. Safe to call any time.
func Stop() {
	sessMu.Lock()
	s := sess
	sess = nil
	sessMu.Unlock()
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.wg != nil {
		s.wg.Close() // also closes the adapter
	} else {
		_ = s.tun.Close()
	}
	if s.done != nil {
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			log.Printf("MASQUE loop still shutting down, leaving it")
		}
	}
	s.bypass.remove()
	masqueUp.Store(false)
	log.Printf("tunnel stopped: %s", s.mode.ID)
}

// Stats describes the running tunnel.
type Stats struct {
	Running   bool   `json:"running"`
	Mode      string `json:"mode,omitempty"`
	Connected bool   `json:"connected"` // handshake / CONNECT-IP session up
	RX        uint64 `json:"rx"`        // bytes delivered to Windows
	TX        uint64 `json:"tx"`        // bytes sent into the tunnel
	Seconds   int    `json:"seconds"`
}

// CurrentStats reports the running tunnel, or Running=false.
func CurrentStats() Stats {
	sessMu.Lock()
	s := sess
	sessMu.Unlock()
	if s == nil {
		return Stats{}
	}
	connected := masqueUp.Load()
	if s.wg != nil {
		connected = wgHandshakeDone(s.wg)
	}
	return Stats{
		Running:   true,
		Mode:      s.mode.ID,
		Connected: connected,
		RX:        s.tun.rx.Load(),
		TX:        s.tun.tx.Load(),
		Seconds:   int(time.Since(s.started).Seconds()),
	}
}

func hostPrefixes(v4, v6 string) []netip.Prefix {
	var out []netip.Prefix
	if a, err := netip.ParseAddr(stripPrefix(v4)); err == nil && a.Is4() {
		out = append(out, netip.PrefixFrom(a, 32))
	}
	if a, err := netip.ParseAddr(stripPrefix(v6)); err == nil && a.Is6() {
		out = append(out, netip.PrefixFrom(a, 128))
	}
	return out
}

func hasV6(p []netip.Prefix) bool {
	for _, x := range p {
		if x.Addr().Is6() {
			return true
		}
	}
	return false
}

// configureAdapter gives the adapter its addresses, routes, MTU, DNS and the
// lowest metric, so Windows prefers it (and its DNS servers) over Wi-Fi.
func configureAdapter(luid winipcfg.LUID, addrs, routes []netip.Prefix) error {
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		is4 := family == windows.AF_INET
		var famAddrs []netip.Prefix
		for _, a := range addrs {
			if a.Addr().Is4() == is4 {
				famAddrs = append(famAddrs, a)
			}
		}
		var famRoutes []*winipcfg.RouteData
		for _, r := range routes {
			if r.Addr().Is4() != is4 {
				continue
			}
			nh := netip.IPv6Unspecified()
			if is4 {
				nh = netip.IPv4Unspecified()
			}
			famRoutes = append(famRoutes, &winipcfg.RouteData{Destination: r.Masked(), NextHop: nh, Metric: 0})
		}
		if len(famAddrs) == 0 {
			continue
		}
		err := luid.SetIPAddressesForFamily(family, famAddrs)
		if err == windows.ERROR_OBJECT_ALREADY_EXISTS {
			cleanupStaleAddresses(family, famAddrs)
			err = luid.SetIPAddressesForFamily(family, famAddrs)
		}
		if err != nil {
			return fmt.Errorf("set tunnel address: %w", err)
		}
		if err := luid.SetRoutesForFamily(family, famRoutes); err != nil {
			return fmt.Errorf("set tunnel routes: %w", err)
		}
		ipif, err := luid.IPInterface(family)
		if err != nil {
			return err
		}
		ipif.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		ipif.DadTransmits = 0
		ipif.ManagedAddressConfigurationSupported = false
		ipif.OtherStatefulConfigurationSupported = false
		ipif.NLMTU = tunnelMTU
		ipif.UseAutomaticMetric = false
		ipif.Metric = 0
		if err := ipif.Set(); err != nil {
			return fmt.Errorf("set adapter metric and MTU: %w", err)
		}
		dns := dnsV6
		if is4 {
			dns = dnsV4
		}
		if err := luid.SetDNS(family, dns, nil); err != nil {
			return fmt.Errorf("set DNS: %w", err)
		}
	}
	return nil
}

// cleanupStaleAddresses removes our tunnel address from adapters that are
// down (left behind by a crash), the same fix wireguard-windows applies.
func cleanupStaleAddresses(family winipcfg.AddressFamily, addrs []netip.Prefix) {
	want := map[netip.Addr]bool{}
	for _, a := range addrs {
		want[a.Addr()] = true
	}
	ifaces, err := winipcfg.GetAdaptersAddresses(family, winipcfg.GAAFlagDefault)
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.OperStatus == winipcfg.IfOperStatusUp {
			continue
		}
		for a := iface.FirstUnicastAddress; a != nil; a = a.Next {
			if ip, _ := netip.AddrFromSlice(a.Address.IP()); want[ip.Unmap()] {
				_ = iface.LUID.DeleteIPAddress(netip.PrefixFrom(ip.Unmap(), int(a.OnLinkPrefixLength)))
			}
		}
	}
}

// ---- endpoint bypass route -------------------------------------------------

// bypassRoute is a /32 host route that sends the tunnel's own packets to the
// Cloudflare endpoint out of the real network adapter, so they don't loop
// back into the tunnel. It follows the PC to new networks (Wi-Fi roaming,
// plugging in Ethernet) by checking the default route every few seconds.
type bypassRoute struct {
	mu       sync.Mutex
	dest     netip.Prefix
	luid     winipcfg.LUID // real adapter the route is on
	nextHop  netip.Addr
	ourLUID  winipcfg.LUID
	stop     chan struct{}
	stopOnce sync.Once
}

func newBypassRoute(endpoint netip.Addr) (*bypassRoute, error) {
	b := &bypassRoute{dest: netip.PrefixFrom(endpoint, endpoint.BitLen()), stop: make(chan struct{})}
	luid, nh, err := DefaultRoute(0)
	if err != nil {
		return nil, err
	}
	if err := b.install(luid, nh); err != nil {
		return nil, fmt.Errorf("route to Cloudflare: %w", err)
	}
	return b, nil
}

func (b *bypassRoute) install(luid winipcfg.LUID, nh netip.Addr) error {
	_ = luid.DeleteRoute(b.dest, nh) // stale copy from a crash
	if err := luid.AddRoute(b.dest, nh, 0); err != nil && err != windows.ERROR_OBJECT_ALREADY_EXISTS {
		return err
	}
	b.luid, b.nextHop = luid, nh
	return nil
}

func (b *bypassRoute) watch() {
	go func() {
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-b.stop:
				return
			case <-tick.C:
			}
			luid, nh, err := DefaultRoute(b.ourLUID)
			if err != nil {
				continue // offline for a moment; keep the old route
			}
			b.mu.Lock()
			if luid != b.luid || nh != b.nextHop {
				log.Printf("network changed, moving the Cloudflare route")
				_ = b.luid.DeleteRoute(b.dest, b.nextHop)
				if err := b.install(luid, nh); err != nil {
					log.Printf("route to Cloudflare: %v", err)
				}
			}
			b.mu.Unlock()
		}
	}()
}

func (b *bypassRoute) remove() {
	if b == nil {
		return
	}
	b.stopOnce.Do(func() { close(b.stop) })
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.luid != 0 {
		_ = b.luid.DeleteRoute(b.dest, b.nextHop)
		b.luid = 0
	}
}

// DefaultRoute finds the IPv4 default route Windows would use, ignoring the
// adapter `exclude`: the real adapter and its gateway.
func DefaultRoute(exclude winipcfg.LUID) (winipcfg.LUID, netip.Addr, error) {
	rows, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return 0, netip.Addr{}, err
	}
	best := ^uint64(0)
	var luid winipcfg.LUID
	var nh netip.Addr
	for i := range rows {
		r := &rows[i]
		if r.DestinationPrefix.PrefixLength != 0 || r.InterfaceLUID == exclude {
			continue
		}
		ifrow, err := r.InterfaceLUID.Interface()
		if err != nil || ifrow.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		if strings.EqualFold(ifrow.Alias(), AdapterName) {
			continue
		}
		ipif, err := r.InterfaceLUID.IPInterface(windows.AF_INET)
		if err != nil {
			continue
		}
		if m := uint64(r.Metric) + uint64(ipif.Metric); m < best {
			best, luid, nh = m, r.InterfaceLUID, r.NextHop.Addr()
		}
	}
	if luid == 0 {
		return 0, netip.Addr{}, errors.New("no internet connection (Windows has no default route)")
	}
	return luid, nh, nil
}

// NetworkKey identifies the network the PC is on, so Auto can remember what
// worked there: the adapter kind, its DNS suffix, DNS servers and gateway.
func NetworkKey() string {
	luid, gw, err := DefaultRoute(0)
	if err != nil {
		return ""
	}
	ifaces, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		return ""
	}
	for _, a := range ifaces {
		if a.LUID != luid {
			continue
		}
		kind := "eth"
		if a.IfType == winipcfg.IfTypeIEEE80211 {
			kind = "wifi"
		}
		var dns []string
		for d := a.FirstDNSServerAddress; d != nil; d = d.Next {
			if ip := d.Address.IP(); ip != nil {
				dns = append(dns, ip.String())
			}
		}
		return kind + "|" + a.DNSSuffix() + "|" + strings.Join(dns, ",") + "|" + gw.String()
	}
	return ""
}
