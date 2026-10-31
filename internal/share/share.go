// Package share makes a site reachable from the internet for a while (`flock share`), through a
// Cloudflare quick tunnel: a random https://<words>.trycloudflare.com address, no account needed.
//
// It is for showing work in progress, never for production: a share lasts at most Lifetime, isn't
// kept when the daemon stops, and only the site itself is reachable (see web.Share for what the
// web server does to keep it that way). With Allow, Cloudflare asks visitors for a one-time code
// sent to an allowed email address before they get through (cloudflared's --allowed-mail).
package share

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flock/internal/binaries"
	"flock/internal/supervisor"
)

// Lifetime is how long a share lasts. There is no setting: a preview link shouldn't outlive the
// working day it was made for.
const Lifetime = 8 * time.Hour

// startTimeout is how long cloudflared gets to report its address, and connectTimeout how long
// it then gets to connect to Cloudflare: until it does, visitors get Cloudflare's error 1033.
var (
	startTimeout   = 90 * time.Second
	connectTimeout = 30 * time.Second
)

// States of a share.
const (
	Building = "building" // building the assets (npm run build)
	Starting = "starting" // downloading cloudflared, or waiting for the tunnel's address and connection
	Running  = "running"
	Failed   = "failed" // see Error; the share stays listed until it is stopped or started again
)

// Status is one share, as the API reports it.
type Status struct {
	Site      string     `json:"site"`
	State     string     `json:"state"`
	URL       string     `json:"url,omitempty"`
	Allow     []string   `json:"allow"` // emails (or *@domain) allowed in; empty: anyone with the URL
	Debug     bool       `json:"debug"` // debug mode is on for visitors (stack traces), whatever .env says
	StartedAt time.Time  `json:"started_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // set once running
	Built     bool       `json:"built"`                // the assets were built for the share
	// CanRebuild is set for projects whose assets Flock builds (Vite); Rebuilding while it does,
	// and RebuildError when the last rebuild failed.
	CanRebuild   bool   `json:"can_rebuild"`
	Rebuilding   bool   `json:"rebuilding"`
	RebuildError string `json:"rebuild_error,omitempty"`
	Error        string `json:"error,omitempty"`
	// Warning is what the user should know about how the share protects the app, e.g. that the
	// cached config keeps debug mode as the app has it.
	Warning string `json:"warning,omitempty"`
	Log     string `json:"log"`          // name for /v1/logs/services/{name}
	QR      string `json:"qr,omitempty"` // the URL as a QR code: a base64 PNG
}

// Request starts a share.
type Request struct {
	Site    string
	Allow   []string
	Debug   bool   // turn the app's debug mode on for visitors
	Warning string // see Status.Warning
	// Build builds the assets, writing its output to log; nil for projects without a build Flock
	// runs. BuildFirst builds them before sharing (they are missing or out of date).
	Build      func(ctx context.Context, log io.Writer) error
	BuildFirst bool
}

// Serving is a share the web server must serve on Port.
type Serving struct {
	Site  string
	Port  int
	Debug bool // see Request.Debug
}

// Errors of Start; the API maps them to HTTP status codes.
var (
	ErrShared  = errors.New("is already shared")
	ErrInvalid = errors.New("is not an email address or *@domain")
)

// Manager runs the shares, one per site.
type Manager struct {
	root    string
	catalog *binaries.Catalog
	http    *http.Client
	// Changed is called (never with the manager's lock held) when the web server must start or
	// stop serving a share: the daemon reloads.
	Changed func()

	mu     sync.Mutex
	shares map[string]*share
}

type share struct {
	Status
	port   int // set while the web server serves the share
	build  func(context.Context, io.Writer) error
	ctx    context.Context // ends with the share
	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager creates a manager installing cloudflared under root from catalog. Tunnels a crashed
// daemon left behind are stopped.
func NewManager(root string, catalog *binaries.Catalog) *Manager {
	killOrphans(runDir(root))
	return &Manager{root: root, catalog: catalog, http: &http.Client{}, shares: map[string]*share{}}
}

func runDir(root string) string { return filepath.Join(root, "run") }

// LogName is the log of a site's share, for `flock logs`.
func LogName(site string) string { return "share-" + site }

func (m *Manager) logPath(site string) string {
	return filepath.Join(m.root, "log", LogName(site)+".log")
}

func (m *Manager) pidFile(site string) string {
	return filepath.Join(runDir(m.root), LogName(site)+".pid")
}

// email is what --allowed-mail takes: an address, or *@domain for everyone at a domain.
var email = regexp.MustCompile(`^(\*|[A-Za-z0-9.!#$%&'+/=?^_{|}~][A-Za-z0-9.!#$%&'+/=?^_{|}~-]*)@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$`)

// NormalizeAllow checks and lowercases the allowed emails, dropping duplicates.
func NormalizeAllow(allow []string) ([]string, error) {
	out := []string{}
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if !email.MatchString(a) || len(a) > 254 {
			return nil, fmt.Errorf("%q %w", a, ErrInvalid)
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// Start shares a site. It returns once the share is under way; Get follows it. A failed share of
// the site is replaced; a live one is an error, unless it's the same request.
func (m *Manager) Start(req Request) error {
	allow, err := NormalizeAllow(req.Allow)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if old, ok := m.shares[req.Site]; ok {
		if old.State != Failed {
			same := slices.Equal(old.Allow, allow) && old.Debug == req.Debug
			m.mu.Unlock()
			if same {
				return nil
			}
			return fmt.Errorf("%s %w with other settings — stop that share first", req.Site, ErrShared)
		}
		delete(m.shares, req.Site)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &share{
		Status: Status{Site: req.Site, State: Starting, Allow: allow, Debug: req.Debug, Warning: req.Warning, StartedAt: time.Now().UTC(), Log: LogName(req.Site)},
		ctx:    ctx, cancel: cancel, done: make(chan struct{}),
	}
	s.build, s.CanRebuild = req.Build, req.Build != nil
	if req.Build != nil && req.BuildFirst {
		s.State, s.Built = Building, true
	}
	m.shares[req.Site] = s
	m.mu.Unlock()

	var first func(context.Context, io.Writer) error
	if req.BuildFirst {
		first = req.Build
	}
	go m.run(ctx, s, first)
	return nil
}

// Stop ends a site's share (and forgets a failed one). It reports whether there was one.
func (m *Manager) Stop(site string) bool {
	m.mu.Lock()
	s, ok := m.shares[site]
	m.mu.Unlock()
	if !ok {
		return false
	}
	s.cancel()
	<-s.done
	m.mu.Lock()
	if m.shares[site] == s {
		delete(m.shares, site)
	}
	m.mu.Unlock()
	return true
}

// Rebuild builds a running share's assets again, so visitors get the latest changes when they
// reload. It returns at once; Rebuilding is set while the build runs.
func (m *Manager) Rebuild(site string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.shares[site]
	switch {
	case !ok:
		return fmt.Errorf("%s isn't shared", site)
	case s.build == nil:
		return fmt.Errorf("%s has no asset build Flock runs", site)
	case s.State != Running:
		return fmt.Errorf("%s's share isn't running", site)
	case s.Rebuilding:
		return nil
	}
	s.Rebuilding, s.RebuildError = true, ""
	go func() {
		log, err := os.OpenFile(m.logPath(site), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(log, "[flock] %s rebuilding the assets: npm run build\n", time.Now().Format(time.RFC3339))
			err = s.build(s.ctx, log)
			log.Close()
		}
		m.update(s, func(s *share) {
			s.Rebuilding = false
			if err != nil && s.ctx.Err() == nil {
				s.RebuildError = fmt.Sprintf("building the assets failed (%v) — see `flock logs %s`", err, s.Log)
			}
		})
	}()
	return nil
}

// StopAll ends every share (the daemon is stopping).
func (m *Manager) StopAll() {
	for _, st := range m.List() {
		m.Stop(st.Site)
	}
}

// Retain stops the shares of sites that are no longer served.
func (m *Manager) Retain(sites map[string]bool) {
	for _, st := range m.List() {
		if !sites[st.Site] {
			go m.Stop(st.Site)
		}
	}
}

// Get is a site's share, or nil.
func (m *Manager) Get(site string) *Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.shares[site]; ok {
		st := s.Status
		st.Allow = slices.Clone(st.Allow)
		return &st
	}
	return nil
}

// List is every share, by site.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Status{}
	for _, s := range m.shares {
		st := s.Status
		st.Allow = slices.Clone(st.Allow)
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.Site, b.Site) })
	return out
}

// Serving are the shares the web server must serve.
func (m *Manager) Serving() []Serving {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Serving
	for _, s := range m.shares {
		if s.port != 0 {
			out = append(out, Serving{Site: s.Site, Port: s.port, Debug: s.Debug})
		}
	}
	slices.SortFunc(out, func(a, b Serving) int { return strings.Compare(a.Site, b.Site) })
	return out
}

func (m *Manager) update(s *share, f func(*share)) {
	m.mu.Lock()
	f(s)
	m.mu.Unlock()
}

func (m *Manager) changed() {
	if m.Changed != nil {
		m.Changed()
	}
}

// run takes a share from building to running, and ends it when it is stopped, expires or the
// tunnel exits.
func (m *Manager) run(ctx context.Context, s *share, build func(context.Context, io.Writer) error) {
	defer close(s.done)

	log, err := openLog(m.logPath(s.Site))
	if err != nil {
		m.fail(s, err.Error())
		return
	}
	defer log.Close()
	fmt.Fprintf(log, "[flock] %s sharing %s\n", time.Now().Format(time.RFC3339), s.Site)

	if build != nil {
		fmt.Fprintln(log, "[flock] building the assets: npm run build")
		if err := build(ctx, log); err != nil {
			if ctx.Err() == nil {
				m.fail(s, fmt.Sprintf("building the assets failed (%v) — see `flock logs %s`", err, s.Log))
			}
			return
		}
		m.update(s, func(s *share) { s.State = Starting })
	}

	bin, err := ensure(ctx, m.root, m.catalog, m.http)
	if err != nil {
		if ctx.Err() == nil {
			m.fail(s, err.Error())
		}
		return
	}

	port, err := freePort()
	if err != nil {
		m.fail(s, err.Error())
		return
	}
	m.update(s, func(s *share) { s.port = port })
	m.changed()
	defer func() {
		m.update(s, func(s *share) { s.port = 0 })
		m.changed()
	}()
	if !listening(port, 5*time.Second) {
		m.fail(s, "the web server didn't start serving the share — see `flock logs web`")
		return
	}

	m.tunnel(ctx, s, bin, port, log)
}

// tunnel runs cloudflared until ctx ends, the share expires or cloudflared exits.
func (m *Manager) tunnel(ctx context.Context, s *share, bin string, port int, log io.Writer) {
	// An empty config of Flock's own: cloudflared would otherwise read ~/.cloudflared/config.yml,
	// and a named tunnel configured there would stop the quick tunnel from starting.
	config := filepath.Join(m.root, "cloudflared", "config.yml")
	if err := os.WriteFile(config, []byte("# Flock's quick tunnels use no settings from here.\n"), 0o600); err != nil {
		m.fail(s, err.Error())
		return
	}
	args := []string{"tunnel", "--config", config, "--no-autoupdate", "--metrics", "127.0.0.1:0",
		"--url", "http://127.0.0.1:" + strconv.Itoa(port)}
	for _, a := range s.Allow {
		args = append(args, "--allowed-mail="+a)
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = filepath.Dir(bin)
	out, err := cmd.StderrPipe()
	if err != nil {
		m.fail(s, err.Error())
		return
	}
	cmd.Stdout = log
	supervisor.Configure(cmd)
	if err := cmd.Start(); err != nil {
		m.fail(s, "start cloudflared: "+err.Error())
		return
	}
	supervisor.Started(cmd)
	_ = os.MkdirAll(runDir(m.root), 0o700)
	_ = os.WriteFile(m.pidFile(s.Site), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	defer os.Remove(m.pidFile(s.Site))

	found := make(chan string, 1)
	connected := make(chan struct{})
	var blocked atomic.Bool
	last := make(chan string, 1)
	go func() {
		lastLine := ""
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := sc.Text()
			fmt.Fprintln(log, line)
			if m := tunnelURL.FindStringSubmatch(line); m != nil {
				u := m[1]
				select {
				case found <- u:
				default:
				}
			}
			switch {
			case registered.MatchString(line):
				select {
				case <-connected:
				default:
					close(connected)
				}
			case precheckFailed.MatchString(line):
				blocked.Store(true)
			}
			if msg := strings.TrimSpace(line); msg != "" && !strings.Contains(msg, "+---") && !strings.Contains(msg, "|") {
				lastLine = msg
			}
		}
		last <- lastLine
	}()
	exited := make(chan error, 1)
	go func() {
		<-last // the pipe is read to the end before Wait closes it
		exited <- cmd.Wait()
	}()

	stop := func() {
		_ = cmd.Process.Kill()
		<-exited
	}

	var u string
	select {
	case u = <-found:
	case <-time.After(startTimeout):
		stop()
		m.fail(s, fmt.Sprintf("cloudflared didn't report the tunnel's address — see `flock logs %s`", s.Log))
		return
	case <-ctx.Done():
		stop()
		return
	case err := <-exited:
		m.fail(s, fmt.Sprintf("cloudflared exited (%v) — see `flock logs %s`", err, s.Log))
		return
	}

	// The address exists before the tunnel does: it is only shown once cloudflared has connected.
	select {
	case <-connected:
	case <-time.After(connectTimeout):
		stop()
		m.fail(s, connectError(blocked.Load(), s.Log))
		return
	case <-ctx.Done():
		stop()
		return
	case err := <-exited:
		m.fail(s, fmt.Sprintf("cloudflared exited (%v) — see `flock logs %s`", err, s.Log))
		return
	}
	qr, _ := QRPNG(u, 8)
	expires := time.Now().UTC().Add(Lifetime)
	m.update(s, func(s *share) { s.State, s.URL, s.QR, s.ExpiresAt, s.Error = Running, u, qr, &expires, "" })
	fmt.Fprintf(log, "[flock] shared at %s until %s\n", u, expires.Local().Format(time.Kitchen))

	expire := time.NewTimer(Lifetime)
	defer expire.Stop()
	select {
	case <-ctx.Done():
		stop()
	case <-expire.C:
		stop()
		fmt.Fprintf(log, "[flock] the share expired after %s\n", Lifetime)
		m.mu.Lock()
		if m.shares[s.Site] == s {
			delete(m.shares, s.Site)
		}
		m.mu.Unlock()
	case err := <-exited:
		m.fail(s, fmt.Sprintf("the tunnel closed (%v) — see `flock logs %s`", err, s.Log))
	}
}

// tunnelURL is how cloudflared names a quick tunnel's address.
var tunnelURL = regexp.MustCompile(`(https://[a-z0-9-]+\.trycloudflare\.com)(?:[^a-zA-Z0-9.-]|$)`)

// registered is cloudflared connecting to Cloudflare's edge; precheckFailed its check of the
// network at start finding the edge unreachable (outbound port 7844 blocked, UDP and TCP).
var (
	registered     = regexp.MustCompile(`\bRegistered tunnel connection\b`)
	precheckFailed = regexp.MustCompile(`\bprecheck complete\b.*\bhard_fail=true\b`)
)

// connectError says why a tunnel didn't connect: the network blocks it, when cloudflared's
// precheck found so, or else some other reason the log may tell.
func connectError(blocked bool, log string) string {
	if blocked {
		return fmt.Sprintf("this network blocks Cloudflare's tunnels (outbound port 7844, UDP and TCP) — "+
			"try another network, or turn off the VPN or firewall in the way; see `flock logs %s`", log)
	}
	return fmt.Sprintf("cloudflared couldn't connect to Cloudflare — see `flock logs %s`", log)
}

func (m *Manager) fail(s *share, msg string) {
	m.update(s, func(s *share) {
		s.State, s.Error, s.URL, s.QR, s.ExpiresAt = Failed, msg, "", "", nil
	})
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o600)
}

// freePort is a loopback port nothing listens on, for the share's web server.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func listening(port int, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
