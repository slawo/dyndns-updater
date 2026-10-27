package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	urls                                  []string
	update, user, password, state, listen string
	interval                              time.Duration
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func loadConfig() (config, error) {
	c := config{update: os.Getenv("DYNDNS_UPDATE_URL"), user: os.Getenv("DYNDNS_USERNAME"), password: os.Getenv("DYNDNS_PASSWORD"), state: os.Getenv("DYNDNS_STATE_FILE"), listen: os.Getenv("DYNDNS_LISTEN"), interval: 5 * time.Minute}
	if !strings.Contains(c.update, "{ip}") || !validURL(strings.ReplaceAll(c.update, "{ip}", "1.1.1.1")) {
		return c, errors.New("DYNDNS_UPDATE_URL must be HTTPS, without embedded credentials, and contain {ip}")
	}
	if (c.user == "") != (c.password == "") {
		return c, errors.New("set both DYNDNS_USERNAME and DYNDNS_PASSWORD")
	}
	if c.state == "" || !filepath.IsAbs(c.state) {
		return c, errors.New("DYNDNS_STATE_FILE must be an absolute path outside the repository")
	}
	if c.listen == "" {
		c.listen = ":8080"
	}
	if v := os.Getenv("DYNDNS_INTERVAL"); v != "" {
		var err error
		c.interval, err = time.ParseDuration(v)
		if err != nil || c.interval < time.Second {
			return c, errors.New("DYNDNS_INTERVAL must be a duration of at least 1s")
		}
	}
	raw := os.Getenv("DYNDNS_LOOKUP_URLS")
	if raw == "" {
		p := os.Getenv("DYNDNS_LOOKUP_FILE")
		if p == "" {
			p = "urls.txt"
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return c, errors.New("cannot read DYNDNS_LOOKUP_FILE")
		}
		raw = string(b)
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.SplitN(line, "#", 2)[0]
		for _, v := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			if !validURL(v) {
				return c, errors.New("lookup URLs must be HTTPS without credentials")
			}
			c.urls = append(c.urls, v)
		}
	}
	if len(c.urls) == 0 {
		return c, errors.New("lookup URL list is empty")
	}
	return c, nil
}
func publicIPv4(raw string) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return "", errors.New("response is not a public IPv4 address")
	}
	return ip.String(), nil
}
func fetch(ctx context.Context, client *http.Client, raw, user, password string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", errors.New("invalid request")
	}
	req.Header.Set("User-Agent", "dyndns-updater/1.0")
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("HTTP request failed")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Print("response close failed")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(b) > 4096 {
		return "", errors.New("invalid response body")
	}
	return strings.TrimSpace(string(b)), nil
}
func saveState(path, ip string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".dyndns-*")
	if err != nil {
		return errors.New("cannot create state file")
	}
	defer func() {
		if err := os.Remove(f.Name()); err != nil && !os.IsNotExist(err) {
			log.Print("temporary state cleanup failed")
		}
	}()
	_, writeErr := f.WriteString(ip + "\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("cannot write state file")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return errors.New("cannot replace state file")
	}
	return nil
}

type worker struct {
	config config
	client *http.Client
	last   string
	next   int
}

func (w *worker) tick(ctx context.Context) error {
	source := w.config.urls[w.next]
	w.next = (w.next + 1) % len(w.config.urls)
	raw, err := fetch(ctx, w.client, source, "", "")
	if err != nil {
		return fmt.Errorf("lookup: %w", err)
	}
	ip, err := publicIPv4(raw)
	if err != nil {
		return err
	}
	if ip == w.last {
		log.Print("IPv4 unchanged")
		return nil
	}
	body, err := fetch(ctx, w.client, strings.ReplaceAll(w.config.update, "{ip}", ip), w.config.user, w.config.password)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	fields := strings.Fields(body)
	if len(fields) != 2 || (fields[0] != "good" && fields[0] != "nochg") || fields[1] != ip {
		return errors.New("update did not confirm the requested IPv4")
	}
	if err := saveState(w.config.state, ip); err != nil {
		return err
	}
	w.last = ip
	log.Print("IPv4 published and saved")
	return nil
}

type activity struct {
	sync.Mutex
	status, reason   string
	checks, failures uint64
}

func (a *activity) serve(w http.ResponseWriter, r *http.Request) {
	a.Lock()
	defer a.Unlock()
	if r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		if a.status != "healthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if _, err := fmt.Fprintf(w, `[{"module":"updater","status":%s,"reason":%s}]`, strconv.Quote(a.status), strconv.Quote(a.reason)); err != nil {
			log.Print("health response failed")
		}
		return
	}
	if r.URL.Path == "/metrics" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if _, err := fmt.Fprintf(w, "dyndns_checks_total %d\ndyndns_failures_total %d\n", a.checks, a.failures); err != nil {
			log.Print("metrics response failed")
		}
		return
	}
	http.NotFound(w, r)
}
func Run(ctx context.Context) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", addr)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirects disabled") }}
	w := worker{config: c, client: client}
	b, err := os.ReadFile(c.state)
	if err == nil {
		w.last, err = publicIPv4(string(b))
		if err != nil {
			return errors.New("invalid saved state")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot read saved state")
	}
	a := &activity{status: "down", reason: "waiting for first check"}
	listener, err := net.Listen("tcp", c.listen)
	if err != nil {
		return errors.New("cannot bind DYNDNS_LISTEN")
	}
	server := &http.Server{Handler: http.HandlerFunc(a.serve), ReadHeaderTimeout: 5 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			log.Print("telemetry close failed")
		}
	}()
	log.Printf("health and Prometheus listening on %s at /health and /metrics", listener.Addr())
	for {
		err := w.tick(ctx)
		if ctx.Err() != nil {
			return nil
		}
		a.Lock()
		a.checks++
		a.status = "healthy"
		a.reason = ""
		if err != nil {
			a.status = "unhealthy"
			a.reason = err.Error()
			a.failures++
			log.Print(err)
		}
		a.Unlock()
		// A failed check is deliberately retried at the next interval with the next source.
		timer := time.NewTimer(c.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case err := <-serverErr:
			timer.Stop()
			if !errors.Is(err, http.ErrServerClosed) {
				return errors.New("telemetry server failed")
			}
			return nil
		case <-timer.C:
		}
	}
}
