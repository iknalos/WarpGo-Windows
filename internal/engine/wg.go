package engine

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/Diniboy1123/usque/api"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

func masqueMode(http2 bool) string {
	if http2 {
		return "masque-h2"
	}
	return "masque-h3"
}

func masqueConfig(acct *MasqueAccount, http2 bool) (api.MaintainTunnelConfig, error) {
	tlsCfg, err := masqueTLS(acct)
	if err != nil {
		return api.MaintainTunnelConfig{}, err
	}
	var endpoint net.Addr = &net.UDPAddr{IP: net.ParseIP(acct.EndpointV4), Port: 443}
	if http2 {
		endpoint = &net.TCPAddr{IP: net.ParseIP(acct.EndpointH2V4), Port: 443}
	}
	return api.MaintainTunnelConfig{
		TLSConfig:       tlsCfg,
		KeepalivePeriod: 30 * time.Second,
		Endpoint:        endpoint,
		MTU:             tunnelMTU,
		ReconnectDelay:  time.Second,
		AlwaysReconnect: true,
		UseHTTP2:        http2,
	}, nil
}

// newWgDevice starts WireGuard on t towards engage.cloudflareclient.com:port.
func newWgDevice(t tun.Device, acct *WgAccount, port int, label string) (*device.Device, error) {
	return newWgDeviceAt(t, acct, wgEndpoint(port), label)
}

// newWgDeviceAt starts WireGuard on t towards a resolved ip:port. It closes t
// on failure.
func newWgDeviceAt(t tun.Device, acct *WgAccount, endpoint, label string) (*device.Device, error) {
	priv, err := keyHex(acct.PrivateKey)
	if err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("WireGuard private key: %w", err)
	}
	peer, _ := keyHex(wgPeerKey)
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			if strings.Contains(format, "handshake initiation") || strings.Contains(format, "handshake response") || strings.Contains(format, "Handshake did not complete") {
				log.Printf("wg "+label+": "+format, args...)
			}
		},
		Errorf: func(format string, args ...any) { log.Printf("wg "+label+" error: "+format, args...) },
	}
	dev := device.NewDevice(t, conn.NewDefaultBind(), logger)
	uapi := "private_key=" + priv + "\n" +
		"replace_peers=true\n" +
		"public_key=" + peer + "\n" +
		"endpoint=" + endpoint + "\n" +
		"persistent_keepalive_interval=25\n" +
		"replace_allowed_ips=true\n" +
		"allowed_ip=0.0.0.0/0\n" +
		"allowed_ip=::/0\n"
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, err
	}
	log.Printf("wg %s: endpoint %s", label, endpoint)
	return dev, nil
}

// wgEndpoint resolves engage.cloudflareclient.com (IPv4) and falls back to its
// well-known address if DNS is slow or blocked.
func wgEndpoint(port int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ip := wgFallback
	if ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", wgHost); err == nil && len(ips) > 0 {
		ip = ips[0].String()
	}
	return net.JoinHostPort(ip, fmt.Sprint(port))
}

func wgHandshakeDone(dev *device.Device) bool {
	state, err := dev.IpcGet()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(state, "\n") {
		if v, ok := strings.CutPrefix(line, "last_handshake_time_sec="); ok {
			return v != "0"
		}
	}
	return false
}

func keyHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", err
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("key is %d bytes, want 32", len(raw))
	}
	return hex.EncodeToString(raw), nil
}
