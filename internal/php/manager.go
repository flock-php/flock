package php

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"flock/internal/binaries"
)

// Package and ExtensionsPackage are PHP's names in the binaries manifest. The extension pack
// (Windows only) adds the extensions the official build doesn't ship; there is one per minor.
const (
	Package           = "php"
	ExtensionsPackage = "php-exts"
)

// ErrBusy is returned while a version is already being installed.
var ErrBusy = errors.New("this PHP version is already being installed")

// Job is a running or failed install (the same shape as a FrankenPHP or component job).
type Job struct {
	Action    string    `json:"action"` // install | update
	State     string    `json:"state"`  // running | failed
	Phase     string    `json:"phase"`  // downloading | installing
	Progress  float64   `json:"progress"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"started_at"`
}

// Text describes a running job in a few words: "Downloading PHP 8.4… 42%", "Installing PHP 8.4…".
func (j Job) Text(minor string) string {
	if j.Phase == "installing" {
		return "Installing PHP " + minor + "…"
	}
	if j.Progress >= 0 && j.Progress <= 1 {
		return fmt.Sprintf("Downloading PHP %s… %d%%", minor, int(j.Progress*100+0.5))
	}
	return "Downloading PHP " + minor + "…"
}

// Release is one PHP minor: what is installed and what the channel offers.
type Release struct {
	Minor      string `json:"minor"`
	Latest     string `json:"latest"`    // newest release in the channel, "" when it has none
	Installed  string `json:"installed"` // full version, "" when not installed
	Status     string `json:"status"`    // installed | update | available
	Update     bool   `json:"update"`    // Latest (or a newer build of it) can be installed
	Size       int64  `json:"size"`      // download size of Latest (with the extension pack)
	Prerelease bool   `json:"prerelease"`
	Yanked     string `json:"yanked"` // why the installed build was withdrawn, "" when it wasn't
	Job        *Job   `json:"job"`
}

// Info is what is installed for a minor, kept in php/<minor>/flock.json. Builds are the manifest's
// build numbers, 0 when installed from a local file.
type Info struct {
	Version         string `json:"version"`
	Build           int    `json:"build"`
	ExtensionsBuild int    `json:"extensions_build,omitempty"`
}

func infoFile(root, minor string) string { return filepath.Join(Dir(root, minor), "flock.json") }

// InstalledInfo is what is installed for a minor; ok is false when nothing is.
func InstalledInfo(root, minor string) (Info, bool) {
	if !fileExists(Binary(root, minor)) {
		return Info{}, false
	}
	var info Info
	if data, err := os.ReadFile(infoFile(root, minor)); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	if info.Version == "" {
		info.Version = FullVersion(root, minor)
	}
	return info, true
}

// Manager installs PHP from the binaries manifest, one job per minor at a time.
type Manager struct {
	root     string
	catalog  *binaries.Catalog
	http     *http.Client
	platform string
	// Release stops whatever runs a version before its files are replaced (Windows locks the files
	// of running programs); Finished is called after every install, also a failed one, so the
	// daemon reloads and starts what Release stopped. Either may be nil.
	Release  func(minor string)
	Finished func(minor string)

	mu   sync.Mutex
	jobs map[string]*job
	base context.Context
	wg   sync.WaitGroup
}

type job struct {
	Job
	cancel context.CancelFunc
}

// NewManager creates a manager installing under root from catalog. A withdrawn (yanked) build is
// replaced by the next good one as soon as the manifest has it.
func NewManager(root string, catalog *binaries.Catalog) *Manager {
	m := &Manager{
		root: root, catalog: catalog, http: &http.Client{}, platform: binaries.Platform(),
		jobs: map[string]*job{}, base: context.Background(),
	}
	if catalog != nil {
		catalog.OnChange(m.replaceYanked)
	}
	return m
}

// LogPath is the output of a minor's current or last install.
func (m *Manager) LogPath(minor string) string { return LogPath(m.root, minor) }

// LogPath is the install log of a minor under Flock's data dir root.
func LogPath(root, minor string) string {
	return filepath.Join(root, "log", "php"+minor+"-install.log")
}

func (m *Manager) manifest() *binaries.Manifest {
	if m.catalog == nil {
		return nil
	}
	return m.catalog.Manifest()
}

// offer is what the channel has for a minor: the newest release and, on Windows, its extension pack.
type offer struct {
	version    binaries.Version
	extensions *binaries.Entry
}

// installable reports whether both parts may be installed.
func (o offer) installable() bool {
	return o.version.Installable() && (o.extensions == nil || o.extensions.Installable())
}

// NeedsExtensionPack: on Windows, PHP comes with the extension pack of its minor (the official
// build lacks extensions Flock loads), so a minor without one isn't offered (yet). That happens
// for a release candidate the extensions don't build against yet.
func NeedsExtensionPack(platform string) bool { return platform == "windows-x64" }

func latest(manifest *binaries.Manifest, platform, minor string) (offer, bool) {
	for _, v := range manifest.Versions(Package, platform) {
		if MinorOf(v.Version) != minor {
			continue
		}
		o := offer{version: v}
		for _, e := range manifest.Versions(ExtensionsPackage, platform) {
			if e.Version == minor {
				entry := e.Entry
				o.extensions = &entry
			}
		}
		if o.extensions == nil && NeedsExtensionPack(platform) {
			return offer{}, false
		}
		return o, true
	}
	return offer{}, false
}

// LatestStable is the newest minor the channel offers for installing that isn't a pre-release,
// or "" when it offers none (no manifest yet).
func (m *Manager) LatestStable() string { return latestStable(m.manifest(), m.platform) }

func latestStable(manifest *binaries.Manifest, platform string) string {
	newest := ""
	for _, v := range manifest.Versions(Package, platform) {
		minor := MinorOf(v.Version)
		if minor == "" || (newest != "" && !versionLess(newest, minor)) {
			continue
		}
		if o, ok := latest(manifest, platform, minor); ok && !o.version.Prerelease && o.installable() {
			newest = minor
		}
	}
	return newest
}

// Offered reports whether the channel has a build of minor to install. Without a manifest it
// can't tell, and says yes.
func (m *Manager) Offered(minor string) bool {
	manifest := m.manifest()
	if manifest == nil {
		return true
	}
	o, ok := latest(manifest, m.platform, minor)
	return ok && o.installable()
}

func release(manifest *binaries.Manifest, platform, version string) (binaries.Entry, bool) {
	for _, v := range manifest.Versions(Package, platform) {
		if v.Version == version {
			return v.Entry, true
		}
	}
	return binaries.Entry{}, false
}

func describe(manifest *binaries.Manifest, platform, root, minor string) Release {
	r := Release{Minor: minor, Status: "available"}
	o, ok := latest(manifest, platform, minor)
	if ok {
		r.Latest, r.Size, r.Prerelease = o.version.Version, o.version.Size, o.version.Prerelease
		if o.extensions != nil {
			r.Size += o.extensions.Size
		}
	}
	info, installed := InstalledInfo(root, minor)
	if !installed {
		return r
	}
	r.Installed, r.Status = info.Version, "installed"
	if r.Installed == "" {
		r.Installed = "unknown"
	}
	if e, found := release(manifest, platform, info.Version); found && info.Build > 0 {
		r.Yanked = e.YankReason(info.Build)
	}
	if ok && o.installable() {
		// A local install (build 0) is replaced by the published build of the same release.
		c := binaries.CompareVersions(o.version.Version, info.Version)
		r.Update = c > 0 || (c == 0 && (info.Build == 0 || o.version.Build > info.Build ||
			(o.extensions != nil && o.extensions.Build > info.ExtensionsBuild)))
	}
	if r.Update {
		r.Status = "update"
	}
	return r
}

// CheckedAt is when the manifest was last fetched and the error, if any.
func (m *Manager) CheckedAt() (time.Time, string) {
	if m.catalog == nil {
		return time.Time{}, ""
	}
	cs := m.catalog.Status()
	if cs.CheckedAt == nil {
		return time.Time{}, cs.CheckError
	}
	return *cs.CheckedAt, cs.CheckError
}

// Releases returns every minor the channel offers or that is installed, newest first.
func (m *Manager) Releases() []Release {
	manifest := m.manifest()
	all := map[string]bool{}
	installed, _ := Installed(m.root)
	for _, minor := range installed {
		all[minor] = true
	}
	for _, v := range manifest.Versions(Package, m.platform) {
		if minor := MinorOf(v.Version); minor != "" {
			if _, ok := latest(manifest, m.platform, minor); ok {
				all[minor] = true
			}
		}
	}
	m.mu.Lock()
	for minor := range m.jobs {
		all[minor] = true
	}
	m.mu.Unlock()

	out := make([]Release, 0, len(all))
	for minor := range all {
		r := describe(manifest, m.platform, m.root, minor)
		r.Job = m.Job(minor)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[j].Minor, out[i].Minor) })
	return out
}

// Job returns the install job of a minor, if there is one.
func (m *Manager) Job(minor string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil {
		copied := j.Job
		return &copied
	}
	return nil
}

// Check fetches the manifest now.
func (m *Manager) Check(ctx context.Context) error {
	if m.catalog == nil {
		return errors.New("PHP isn't published for this platform")
	}
	_, err := m.catalog.Check(ctx)
	return err
}

// replaceYanked installs the next good build for every installed version that was withdrawn.
func (m *Manager) replaceYanked() {
	manifest := m.manifest()
	installed, _ := Installed(m.root)
	for _, minor := range installed {
		if r := describe(manifest, m.platform, m.root, minor); r.Yanked != "" && r.Update && !m.Busy(minor) {
			_ = m.Install(minor)
		}
	}
}

// Run cancels running jobs when ctx ends (the catalog keeps the manifest fresh).
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

// Install installs or updates minor to the channel's newest release, in the background.
func (m *Manager) Install(minor string) error {
	if MinorOf(minor) != minor {
		return fmt.Errorf("invalid PHP version %q", minor)
	}
	if m.catalog == nil || m.platform == "" {
		return errors.New("PHP isn't published for this platform")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil && j.State == "running" {
		return ErrBusy
	}
	action := "install"
	if fileExists(Binary(m.root, minor)) {
		action = "update"
	}
	ctx, cancel := context.WithCancel(m.base)
	j := &job{Job: Job{Action: action, State: "running", Phase: "downloading", Progress: -1, StartedAt: time.Now().UTC()}, cancel: cancel}
	m.jobs[minor] = j
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, minor, j)
	}()
	return nil
}

func (m *Manager) run(ctx context.Context, minor string, j *job) {
	_ = os.MkdirAll(filepath.Dir(m.LogPath(minor)), 0o700)
	var log io.Writer = io.Discard
	if f, err := os.Create(m.LogPath(minor)); err == nil {
		defer f.Close()
		log = f
	}
	set := func(phase string, progress float64) {
		m.mu.Lock()
		j.Phase, j.Progress = phase, progress
		m.mu.Unlock()
	}

	err := m.install(ctx, minor, log, set)

	m.mu.Lock()
	switch {
	case err != nil && ctx.Err() != nil && m.base.Err() == nil:
		fmt.Fprintln(log, "[flock] canceled")
		delete(m.jobs, minor)
	case err != nil:
		fmt.Fprintf(log, "[flock] failed: %v\n", err)
		j.State, j.Error = "failed", err.Error()
	default:
		delete(m.jobs, minor)
	}
	m.mu.Unlock()
	if m.Finished != nil {
		m.Finished(minor)
	}
}

func (m *Manager) install(ctx context.Context, minor string, log io.Writer, set func(string, float64)) error {
	fmt.Fprintf(log, "[flock] looking up PHP %s in the %s channel\n", minor, m.catalog.Status().Channel)
	manifest, err := m.catalog.Check(ctx)
	if err != nil {
		return err
	}
	o, ok := latest(manifest, m.platform, minor)
	if !ok {
		return fmt.Errorf("no PHP %s build is published for this platform", minor)
	}

	base := filepath.Join(m.root, "php")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(base, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	// Progress covers both downloads, weighted by size.
	total := o.version.Size
	if o.extensions != nil {
		total += o.extensions.Size
	}
	var before int64
	progress := func(done, size int64) {
		switch {
		case total > 0:
			set("downloading", float64(before+done)/float64(total))
		case size > 0:
			set("downloading", float64(done)/float64(size))
		default:
			set("downloading", -1)
		}
	}

	fmt.Fprintf(log, "[flock] downloading PHP %s, build %d: %s\n", o.version.Version, o.version.Build, o.version.URL)
	set("downloading", 0)
	dir := filepath.Join(staging, "php")
	if err := binaries.Install(ctx, m.http, o.version.Entry, dir, progress); err != nil {
		return err
	}
	fmt.Fprintf(log, "[flock] verified SHA-256 %s\n", o.version.SHA256)

	info := Info{Version: o.version.Version, Build: o.version.Build}
	if o.extensions != nil {
		before = o.version.Size
		fmt.Fprintf(log, "[flock] downloading the extensions for PHP %s, build %d: %s\n", minor, o.extensions.Build, o.extensions.URL)
		pack := filepath.Join(staging, "exts")
		if err := binaries.Install(ctx, m.http, *o.extensions, pack, progress); err != nil {
			return err
		}
		fmt.Fprintf(log, "[flock] verified SHA-256 %s\n", o.extensions.SHA256)
		if err := MergeExtensionPack(dir, pack); err != nil {
			return err
		}
		info.ExtensionsBuild = o.extensions.Build
	}

	set("installing", -1)
	if err := place(m.root, minor, dir, info, m.Release); err != nil {
		return err
	}
	fmt.Fprintf(log, "[flock] installed PHP %s\n", info.Version)
	return nil
}

// Cancel stops a running install.
func (m *Manager) Cancel(minor string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil && j.State == "running" {
		j.cancel()
	}
}

// Dismiss forgets a failed install.
func (m *Manager) Dismiss(minor string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil && j.State != "running" {
		delete(m.jobs, minor)
	}
}

// Busy reports whether an install for minor is running.
func (m *Manager) Busy(minor string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[minor]
	return j != nil && j.State == "running"
}

// Wait blocks until every job has finished (for tests and shutdown).
func (m *Manager) Wait() { m.wg.Wait() }

// MinorOf turns "8.5.11" (or "8.6.0RC1") into "8.5" ("" if it is not a version).
func MinorOf(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	for _, p := range parts[:2] {
		if _, err := strconv.Atoi(p); err != nil {
			return ""
		}
	}
	return parts[0] + "." + parts[1]
}

// probe reports the version of a php binary (replaced in tests).
var probe = func(binary string) (string, error) {
	out, err := exec.Command(binary, "-n", "-r", "echo PHP_VERSION;").CombinedOutput()
	if err != nil {
		hint := ""
		if runtime.GOOS == "windows" {
			hint = " — it needs the Microsoft Visual C++ Redistributable: https://aka.ms/vs/17/release/vc_redist.x64.exe"
		}
		return "", fmt.Errorf("run %s: %v: %s%s", filepath.Base(binary), err, strings.TrimSpace(string(out)), hint)
	}
	return strings.TrimSpace(string(out)), nil
}
