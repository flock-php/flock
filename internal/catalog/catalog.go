// Package catalog installs and runs the services Flock offers on request besides PostgreSQL:
// MySQL, MariaDB, MongoDB, Redis, Valkey, Meilisearch, Typesense and RustFS. Their builds come
// from the binaries manifest of the configured channel. Each service runs a single version (of a
// line the user picks, like MySQL 8.4) on its own port; installs run as background jobs the apps
// follow by polling.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/ports"
	"flock/internal/supervisor"
)

// Errors of Manager operations; the API maps them to HTTP status codes.
var (
	ErrUnknown     = errors.New("unknown service")
	ErrUnavailable = errors.New("not available on this platform")
	ErrBusy        = errors.New("a job is already running")
	ErrInvalid     = errors.New("invalid request")
)

// Mutate edits config.json and applies it (the daemon's Mutate, or a CLI equivalent).
type Mutate func(change func(*config.Config) error) error

// Install methods (only downloads are left: every service comes from the manifest).
const MethodDownload = "download"

// Job phases.
const (
	PhaseDownloading = "downloading"
	PhaseInstalling  = "installing"
)

// Paths are where an installed version lives.
type Paths struct {
	Root    string // Flock's root
	Dir     string // the installed version
	Data    string // the line's data directory
	Log     string // the service's log file
	Version string
	Line    string
}

// Kind is one service of the catalog.
type Kind struct {
	ID          string
	Name        string
	Category    string
	Description string
	DefaultPort int
	// Package is the service's name in the manifest ("" for ID).
	Package string
	// Line is the release line a version belongs to: "8.4" for MySQL 8.4.11. Versions of a line
	// share its data; newer lines can't be downgraded, so each keeps its own.
	Line func(version string) string
	// Unavailable is why the service isn't offered where the manifest has no build of it.
	Unavailable string

	// Prepare fixes up an unpacked package (removing what Flock doesn't need) before it is used.
	Prepare func(dir string) error
	// Spec prepares the data directory (initialising it the first time) and describes the server.
	Spec func(p Paths, port int) (supervisor.Spec, error)
	// Env is the .env snippet that connects a Laravel app, URL a connection URL ("" for none).
	Env func(port int) string
	URL func(port int) string
}

func (k *Kind) pkg() string {
	if k.Package != "" {
		return k.Package
	}
	return k.ID
}

// LineOf returns the line a version belongs to, "" when none.
func (k *Kind) LineOf(version string) string { return k.Line(version) }

// majorMinor is the line of most services: "8.4" for "8.4.11".
func majorMinor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// major is the line of services that version by major: "30" for Typesense "30.2".
func major(v string) string {
	m, _, _ := strings.Cut(v, ".")
	if _, err := strconv.Atoi(m); err != nil {
		return ""
	}
	return m
}

// Dir is where version of service id is installed.
func Dir(root, id, version string) string { return filepath.Join(root, "services", id, version) }

// DataDir is the data of a line of service id.
func DataDir(root, id, line string) string { return filepath.Join(root, "services", id, "data", line) }

// LogFile is the log of service id.
func LogFile(root, id string) string { return filepath.Join(root, "log", id+".log") }

// PathsOf returns the paths of the installed version of k.
func PathsOf(root string, k *Kind, version string) Paths {
	line := k.LineOf(version)
	return Paths{Root: root, Dir: Dir(root, k.ID, version), Data: DataDir(root, k.ID, line), Log: LogFile(root, k.ID),
		Version: version, Line: line}
}

// Catalog is the state of every service (GET /v1/catalog).
type Catalog struct {
	CheckedAt  *time.Time `json:"checked_at"`
	CheckError string     `json:"check_error"`
	Services   []Entry    `json:"services"`
}

// Find returns the entry of id.
func (c Catalog) Find(id string) (Entry, bool) {
	for _, e := range c.Services {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Entry is one service's state.
type Entry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable"` // why it isn't available here
	Method      string `json:"method"`
	Estimate    string `json:"estimate"`  // download size of the newest line, e.g. "about 170 MB download"
	Installed   string `json:"installed"` // version, "" when not installed
	Line        string `json:"line"`      // of the installed version
	Latest      string `json:"latest"`    // newest release of Line
	Update      bool   `json:"update"`    // Latest (or a newer build of it) can be installed
	Yanked      string `json:"yanked"`    // why the installed build was withdrawn, "" when it wasn't
	// Port is the configured port, or for a service that isn't installed the one Flock suggests:
	// the default unless another service has it.
	Port        int         `json:"port"`
	DefaultPort int         `json:"default_port"`
	Lines       []LineState `json:"lines"`
	Job         *Job        `json:"job"`
}

// LineState is one line the channel offers (or that is installed or has data).
type LineState struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Latest     string `json:"latest"`
	Prerelease bool   `json:"prerelease"`
	Installed  bool   `json:"installed"`
	HasData    bool   `json:"has_data"`
}

// Job is a running or failed install.
type Job struct {
	Action    string    `json:"action"` // install | update
	State     string    `json:"state"`  // running | failed
	Phase     string    `json:"phase"`  // downloading | installing
	Progress  float64   `json:"progress"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
	Line      string    `json:"line"`
}

// Manager runs one install job per service at a time.
type Manager struct {
	root     string
	kinds    []*Kind
	catalog  *binaries.Catalog
	platform string
	mutate   Mutate

	mu   sync.Mutex
	jobs map[string]*job
	base context.Context
	wg   sync.WaitGroup
}

type job struct {
	Job
	cancel context.CancelFunc
}

// NewManager creates a manager for kinds installing from catalog under root. mutate edits
// config.json and applies it. A withdrawn (yanked) build is replaced by the next good one as soon
// as the manifest has it.
func NewManager(root string, kinds []*Kind, catalog *binaries.Catalog, mutate Mutate) *Manager {
	m := &Manager{root: root, kinds: kinds, catalog: catalog, platform: binaries.Platform(), mutate: mutate,
		jobs: map[string]*job{}, base: context.Background()}
	if catalog != nil {
		catalog.OnChange(m.replaceYanked)
	}
	return m
}

// Kinds returns the services of the catalog.
func (m *Manager) Kinds() []*Kind { return m.kinds }

// Kind returns the service id.
func (m *Manager) Kind(id string) (*Kind, error) {
	for _, k := range m.kinds {
		if k.ID == id {
			return k, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknown, id)
}

// LogPath is the output of a service's current or last install.
func (m *Manager) LogPath(id string) string {
	return filepath.Join(m.root, "log", id+"-install.log")
}

func (m *Manager) manifest() *binaries.Manifest {
	if m.catalog == nil {
		return nil
	}
	return m.catalog.Manifest()
}

// offers is the newest build of every line of k the manifest has for this platform.
func (m *Manager) offers(manifest *binaries.Manifest, k *Kind) map[string]binaries.Version {
	out := map[string]binaries.Version{}
	for _, v := range manifest.Versions(k.pkg(), m.platform) { // newest first
		if line := k.LineOf(v.Version); line != "" {
			if _, ok := out[line]; !ok {
				out[line] = v
			}
		}
	}
	return out
}

// Available reports whether the manifest offers k for this platform, and else why not.
func (m *Manager) Available(k *Kind) (bool, string) {
	if len(m.offers(m.manifest(), k)) > 0 {
		return true, ""
	}
	if k.Unavailable != "" {
		return false, k.Unavailable
	}
	return false, fmt.Sprintf("Flock publishes no %s build for this computer.", k.Name)
}

// Catalog returns every service's state without waiting on the network.
func (m *Manager) Catalog() Catalog {
	cfg, err := config.Load(m.root)
	if err != nil {
		cfg = config.Default()
	}
	manifest := m.manifest()
	out := Catalog{Services: []Entry{}}
	if m.catalog != nil {
		cs := m.catalog.Status()
		out.CheckedAt, out.CheckError = cs.CheckedAt, cs.CheckError
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.kinds {
		out.Services = append(out.Services, m.entry(manifest, k, cfg))
	}
	return out
}

// entry must be called with m.mu held.
func (m *Manager) entry(manifest *binaries.Manifest, k *Kind, cfg *config.Config) Entry {
	offers := m.offers(manifest, k)
	e := Entry{
		ID: k.ID, Name: k.Name, Category: k.Category, Description: k.Description,
		Available: len(offers) > 0, DefaultPort: k.DefaultPort, Lines: []LineState{},
	}
	if e.Available {
		e.Method = MethodDownload
	} else {
		_, e.Unavailable = m.Available(k)
	}

	lines := map[string]bool{}
	for line := range offers {
		lines[line] = true
	}
	if x, ok := cfg.Services.Extra(k.ID); ok && installed(m.root, k.ID, x.Version) {
		e.Installed, e.Line, e.Port = x.Version, k.LineOf(x.Version), x.Port
		lines[e.Line] = true
		if offer, ok := offers[e.Line]; ok {
			e.Latest = offer.Version
			e.Update = offer.Installable() && newer(offer.Version, offer.Build, x.Version, x.Build)
		}
		e.Yanked = yankReason(manifest, m.platform, k, x)
	} else {
		e.Port = SuggestPort(cfg, k.ID, k.DefaultPort)
	}
	if entries, err := os.ReadDir(filepath.Join(m.root, "services", k.ID, "data")); err == nil {
		for _, d := range entries {
			if d.IsDir() && hasData(filepath.Join(m.root, "services", k.ID, "data", d.Name())) {
				lines[d.Name()] = true
			}
		}
	}

	ids := make([]string, 0, len(lines))
	for line := range lines {
		ids = append(ids, line)
	}
	sort.Slice(ids, func(i, j int) bool { return binaries.CompareVersions(ids[i], ids[j]) > 0 })
	for _, line := range ids {
		offer := offers[line]
		e.Lines = append(e.Lines, LineState{
			ID: line, Label: line, Latest: offer.Version, Prerelease: offer.Prerelease, Installed: e.Line == line,
			HasData: hasData(DataDir(m.root, k.ID, line)),
		})
	}
	if newest, ok := offers[firstOffered(ids, offers)]; ok {
		e.Estimate = fmt.Sprintf("about %d MB download", max(1, newest.Size/1_000_000))
	}
	if j := m.jobs[k.ID]; j != nil {
		copied := j.Job
		e.Job = &copied
	}
	return e
}

// firstOffered is the newest line of ids the manifest offers.
func firstOffered(ids []string, offers map[string]binaries.Version) string {
	for _, id := range ids {
		if _, ok := offers[id]; ok {
			return id
		}
	}
	return ""
}

// newer reports whether version (build) is newer than the installed one: a later version, or a
// higher build of it. An install from before builds were recorded (build 0) counts as older.
func newer(version string, build int, installed string, installedBuild int) bool {
	if c := binaries.CompareVersions(version, installed); c != 0 {
		return c > 0
	}
	return build > installedBuild
}

// yankReason is why the installed build of k was withdrawn, or "".
func yankReason(manifest *binaries.Manifest, platform string, k *Kind, x config.Extra) string {
	if x.Build == 0 {
		return ""
	}
	for _, v := range manifest.Versions(k.pkg(), platform) {
		if v.Version == x.Version {
			return v.YankReason(x.Build)
		}
	}
	return ""
}

// installed reports whether version of service id is on disk.
func installed(root, id, version string) bool {
	_, err := os.Stat(Dir(root, id, version))
	return err == nil
}

func hasData(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// SuggestPort returns port, or the next one up when another Flock service is configured for it
// or something else listens on it.
func SuggestPort(cfg *config.Config, id string, port int) int {
	used := cfg.UsedPorts(id)
	for p := port; p < port+100 && p <= 65535; p++ {
		if _, taken := used[p]; !taken && ports.Free("127.0.0.1", p) {
			return p
		}
	}
	return port
}

// Check fetches the manifest now.
func (m *Manager) Check(ctx context.Context) error {
	if m.catalog == nil {
		return errors.New("no package manifest")
	}
	_, err := m.catalog.Check(ctx)
	return err
}

// replaceYanked reinstalls every installed service whose build was withdrawn, when a good one exists.
func (m *Manager) replaceYanked() {
	for _, e := range m.Catalog().Services {
		if e.Yanked != "" && e.Update && e.Job == nil {
			_ = m.Start(e.ID, e.Line, 0)
		}
	}
}

// Run cancels running jobs when ctx ends (the binaries catalog keeps the manifest fresh).
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	<-ctx.Done()
	m.mu.Lock()
	for _, j := range m.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	m.mu.Unlock()
}

// Start begins installing the newest build of line (the installed line, else the newest, when
// "") in the background, on port (the configured or suggested one when 0). Installing replaces
// the installed version; each line keeps its own data.
func (m *Manager) Start(id, line string, port int) error {
	k, err := m.Kind(id)
	if err != nil {
		return err
	}
	offers := m.offers(m.manifest(), k)
	if len(offers) == 0 {
		why := k.Unavailable
		if why == "" {
			why = "the package manifest has no build of it for this computer"
		}
		return fmt.Errorf("%s is %w: %s", k.Name, ErrUnavailable, why)
	}
	cfg, err := config.Load(m.root)
	if err != nil {
		return err
	}
	current, isInstalled := cfg.Services.Extra(id)
	if line == "" {
		if isInstalled && offers[k.LineOf(current.Version)].Version != "" {
			line = k.LineOf(current.Version)
		} else {
			lines := make([]string, 0, len(offers))
			for l := range offers {
				lines = append(lines, l)
			}
			sort.Slice(lines, func(i, j int) bool { return binaries.CompareVersions(lines[i], lines[j]) > 0 })
			line = lines[0]
		}
	}
	offer, ok := offers[line]
	if !ok {
		return fmt.Errorf("%w: %s has no line %q", ErrInvalid, k.Name, line)
	}
	if !offer.Installable() {
		return fmt.Errorf("%s %s build %d was withdrawn: %s", k.Name, offer.Version, offer.Build, offer.YankReason(offer.Build))
	}
	if port == 0 {
		port = current.Port
		if !isInstalled || port == 0 {
			port = SuggestPort(cfg, id, k.DefaultPort)
		}
	}
	if err := CheckPort(cfg, id, port); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		return fmt.Errorf("%w for %s", ErrBusy, k.Name)
	}
	action := "install"
	if isInstalled && k.LineOf(current.Version) == line {
		action = "update"
	}
	ctx, cancel := context.WithCancel(m.base)
	j := &job{Job: Job{Action: action, State: "running", Phase: PhaseDownloading, Progress: -1,
		StartedAt: time.Now().UTC(), Version: offer.Version, Line: line}, cancel: cancel}
	m.jobs[id] = j

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, k, j, offer, port)
	}()
	return nil
}

// CheckPort fails when port is outside 1024–65535 or another Flock service is configured for it.
func CheckPort(cfg *config.Config, id string, port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1024 and 65535", ErrInvalid)
	}
	if other, taken := cfg.UsedPorts(id)[port]; taken {
		return fmt.Errorf("%w: port %d is used by Flock's %s service", ErrInvalid, port, other)
	}
	return nil
}

func (m *Manager) run(ctx context.Context, k *Kind, j *job, offer binaries.Version, port int) {
	_ = os.MkdirAll(filepath.Dir(m.LogPath(k.ID)), 0o700)
	var log io.Writer = io.Discard
	if f, err := os.Create(m.LogPath(k.ID)); err == nil {
		defer f.Close()
		log = f
	}
	set := func(phase string, progress float64) {
		m.mu.Lock()
		j.Phase, j.Progress = phase, progress
		m.mu.Unlock()
	}

	fmt.Fprintf(log, "[flock] %s %s %s, build %d, at %s\n", j.Action, k.Name, offer.Version, offer.Build, time.Now().Format(time.RFC3339))
	err := m.install(ctx, k, offer, port, log, set)

	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case err != nil && ctx.Err() != nil && m.base.Err() == nil:
		fmt.Fprintln(log, "[flock] canceled")
		delete(m.jobs, k.ID)
	case err != nil:
		fmt.Fprintf(log, "[flock] failed: %v\n", err)
		j.State, j.Error = "failed", err.Error()
	default:
		fmt.Fprintf(log, "[flock] installed %s %s on port %d\n", k.Name, offer.Version, port)
		delete(m.jobs, k.ID)
	}
}

// install downloads a build next to the installed ones, makes it the service and removes the others.
func (m *Manager) install(ctx context.Context, k *Kind, offer binaries.Version, port int, log io.Writer, set func(string, float64)) error {
	parent := filepath.Join(m.root, "services", k.ID)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	fmt.Fprintf(log, "[flock] downloading %s\n", offer.URL)
	set(PhaseDownloading, 0)
	staged := filepath.Join(tmp, "out")
	err = binaries.Install(ctx, HTTPClient, offer.Entry, staged, func(done, total int64) {
		p := -1.0
		if total > 0 {
			p = float64(done) / float64(total)
		}
		set(PhaseDownloading, p)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "[flock] verified SHA-256 %s\n", offer.SHA256)
	set(PhaseInstalling, -1)
	if k.Prepare != nil {
		if err := k.Prepare(staged); err != nil {
			return err
		}
	}

	// A reinstall (a new build of the same version) replaces the running one: stop it first
	// (Windows locks its files).
	version := offer.Version
	dir := Dir(m.root, k.ID, version)
	if _, err := os.Stat(dir); err == nil {
		if err := m.mutate(func(cfg *config.Config) error { cfg.Services.RemoveExtra(k.ID); return nil }); err != nil {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, dir); err != nil {
		return err
	}

	err = m.mutate(func(cfg *config.Config) error {
		if err := CheckPort(cfg, k.ID, port); err != nil {
			return err
		}
		cfg.Services.SetExtra(k.ID, config.Extra{Version: version, Build: offer.Build, Port: port})
		cfg.Services.SetRunning(k.ID, true)
		return nil
	})
	if err != nil {
		return err
	}
	return removeVersions(m.root, k.ID, version)
}

// removeVersions deletes the installed versions of service id except keep ("" for all).
// Data and work directories stay.
func removeVersions(root, id, keep string) error {
	entries, err := os.ReadDir(filepath.Join(root, "services", id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "data" || e.Name() == keep || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(root, "services", id, e.Name())))
	}
	return errors.Join(errs...)
}

// Cancel stops a running job; it disappears once the job has stopped.
func (m *Manager) Cancel(id string) error {
	if _, err := m.Kind(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		j.cancel()
	}
	return nil
}

// Dismiss forgets a failed job.
func (m *Manager) Dismiss(id string) error {
	if _, err := m.Kind(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil && j.State != "running" {
		delete(m.jobs, id)
	}
	return nil
}

// Uninstall stops and removes a service, and with deleteData the data of every line too.
func (m *Manager) Uninstall(id string, deleteData bool) error {
	k, err := m.Kind(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		m.mu.Unlock()
		return fmt.Errorf("%w for %s", ErrBusy, k.Name)
	}
	delete(m.jobs, id)
	m.mu.Unlock()

	if err := m.mutate(func(cfg *config.Config) error { cfg.Services.RemoveExtra(id); return nil }); err != nil {
		return err
	}
	if deleteData {
		return os.RemoveAll(filepath.Join(m.root, "services", id))
	}
	return removeVersions(m.root, id, "")
}

// Wait blocks until every job has finished (for tests and shutdown).
func (m *Manager) Wait() { m.wg.Wait() }

// Spec describes the server of installed service id on its configured port.
func Spec(root string, k *Kind, x config.Extra) (supervisor.Spec, error) {
	if !installed(root, k.ID, x.Version) {
		return supervisor.Spec{}, fmt.Errorf("version %s is not installed", x.Version)
	}
	p := PathsOf(root, k, x.Version)
	if p.Line == "" {
		return supervisor.Spec{}, fmt.Errorf("version %s is not one Flock offers", x.Version)
	}
	if err := os.MkdirAll(p.Data, 0o700); err != nil {
		return supervisor.Spec{}, err
	}
	spec, err := k.Spec(p, x.Port)
	if err != nil {
		return supervisor.Spec{}, err
	}
	spec.Name, spec.LogFile = k.ID, p.Log
	return spec, nil
}

// HTTPClient downloads the packages (tests replace it).
var HTTPClient = http.DefaultClient
