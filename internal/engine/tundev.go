package engine

import (
	"os"
	"sync"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/tun"
)

// countTun wraps the real network adapter (Wintun on Windows). It satisfies
// both wireguard-go's tun.Device and usque's api.TunnelDevice, and counts the
// bytes in each direction so WarpGo can tell a live tunnel from one that is up
// but passes nothing.
type countTun struct {
	inner     tun.Device
	rx, tx    atomic.Uint64 // rx = delivered to Windows, tx = sent by Windows
	closeOnce sync.Once
	closeErr  error

	readMu  sync.Mutex // ReadPacket reuses one sizes slice
	sizes   []int
	bufs    [][]byte
	writeMu sync.Mutex
	wbufs   [][]byte
}

func newCountTun(inner tun.Device) *countTun {
	return &countTun{inner: inner, sizes: make([]int, 1), bufs: make([][]byte, 1), wbufs: make([][]byte, 1)}
}

func (t *countTun) File() *os.File           { return t.inner.File() }
func (t *countTun) MTU() (int, error)        { return t.inner.MTU() }
func (t *countTun) Name() (string, error)    { return t.inner.Name() }
func (t *countTun) Events() <-chan tun.Event { return t.inner.Events() }
func (t *countTun) BatchSize() int           { return t.inner.BatchSize() }

func (t *countTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	n, err := t.inner.Read(bufs, sizes, offset)
	for i := 0; i < n; i++ {
		t.tx.Add(uint64(sizes[i]))
	}
	return n, err
}

func (t *countTun) Write(bufs [][]byte, offset int) (int, error) {
	n, err := t.inner.Write(bufs, offset)
	for i := 0; i < n && i < len(bufs); i++ {
		t.rx.Add(uint64(len(bufs[i]) - offset))
	}
	return n, err
}

// ReadPacket and WritePacket are the api.TunnelDevice side used by usque.

func (t *countTun) ReadPacket(buf []byte) (int, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()
	t.bufs[0] = buf
	for {
		n, err := t.inner.Read(t.bufs, t.sizes, 0)
		if err != nil {
			return 0, err
		}
		if n == 1 {
			t.tx.Add(uint64(t.sizes[0]))
			return t.sizes[0], nil
		}
	}
}

func (t *countTun) WritePacket(pkt []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.wbufs[0] = pkt
	_, err := t.inner.Write(t.wbufs, 0)
	if err == nil {
		t.rx.Add(uint64(len(pkt)))
	}
	return err
}

func (t *countTun) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.inner.Close() })
	return t.closeErr
}
