package app

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Trace is what Cloudflare reports about the connection it sees
// (https://www.cloudflare.com/cdn-cgi/trace): warp=on means the traffic
// really arrives over WARP.
type Trace struct {
	IP   string `json:"ip"`
	Colo string `json:"colo"` // Cloudflare data centre, e.g. BOS
	Loc  string `json:"loc"`  // country
	Warp string `json:"warp"` // on, plus or off
}

// FetchTrace asks Cloudflare how it sees this PC's traffic.
func FetchTrace(timeout time.Duration) (*Trace, error) {
	return FetchTraceURL("https://www.cloudflare.com/cdn-cgi/trace", timeout)
}

// FetchTraceURL is FetchTrace against another Cloudflare host (the CI test
// uses https://1.0.0.1/cdn-cgi/trace with only that address in the tunnel).
func FetchTraceURL(url string, timeout time.Duration) (*Trace, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "WarpGo-Windows")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("trace: HTTP %d", resp.StatusCode)
	}
	t := &Trace{}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			t.IP = v
		case "colo":
			t.Colo = v
		case "loc":
			t.Loc = v
		case "warp":
			t.Warp = v
		}
	}
	return t, sc.Err()
}
