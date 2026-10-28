package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"flock/internal/binaries"
)

var (
	// ErrUnknownExtension is returned for an extension Flock doesn't offer.
	ErrUnknownExtension = errors.New("unknown extension")
	// ErrExtensionInUse is returned when another installed extension requires the one to remove.
	ErrExtensionInUse = errors.New("needed by")
)

// MajorExtensions is the state of every extension for one major
// (GET /v1/postgres/versions/{major}/extensions).
type MajorExtensions struct {
	Major      string            `json:"major"`
	Extensions []ExtensionState  `json:"extensions"`
	CustomDir  string            `json:"custom_dir"`
	Custom     []CustomExtension `json:"custom"` // what is in CustomDir, read only
}

// ExtensionState is one extension for one major.
type ExtensionState struct {
	Extension
	Supported    bool   `json:"supported"`    // a build for this major and platform exists (or is installed)
	Installed    string `json:"installed"`    // version, e.g. "3.6.4"
	Latest       string `json:"latest"`       // newest version for this major
	Update       bool   `json:"update"`       // Latest (or a newer build of it) can be installed
	Discontinued bool   `json:"discontinued"` // newer versions exist, but not for this major
	Size         int64  `json:"size"`         // download size of Latest
	Prerelease   bool   `json:"prerelease"`
	Yanked       string `json:"yanked"` // why the installed build was withdrawn, "" when it wasn't
	Busy         string `json:"busy"`   // "" | "installing" | "updating" | "uninstalling"
}

// extensionOffer is the newest build of an extension for a major.
func (m *Manager) extensionOffer(manifest *binaries.Manifest, name, major string) (binaries.Version, bool) {
	versions := manifest.Versions(ExtensionPackage(name, major), m.platform)
	if len(versions) == 0 {
		return binaries.Version{}, false
	}
	return versions[0], true
}

// newestExtension is the newest version of an extension for any major.
func (m *Manager) newestExtension(manifest *binaries.Manifest, name string) string {
	newest := ""
	for _, major := range m.majorsOffered(manifest) {
		if v, ok := m.extensionOffer(manifest, name, major); ok && (newest == "" || binaries.CompareVersions(v.Version, newest) > 0) {
			newest = v.Version
		}
	}
	return newest
}

// majorsOffered lists the majors the manifest or this machine knows about.
func (m *Manager) majorsOffered(manifest *binaries.Manifest) []string {
	seen := map[string]bool{}
	for major := range offers(manifest, m.platform) {
		seen[major] = true
	}
	ids, _ := InstalledIDs(m.root())
	for _, id := range ids {
		seen[MajorOf(id)] = true
	}
	var out []string
	for major := range seen {
		out = append(out, major)
	}
	return out
}

// Extensions returns the state of every extension for major. It never waits on the network.
func (m *Manager) Extensions(major string) (MajorExtensions, error) {
	if !majorPattern.MatchString(major) {
		return MajorExtensions{}, fmt.Errorf("%w %q: give a major version like %s", ErrInvalidMajor, major, DefaultMajor)
	}
	manifest := m.manifest()
	active, _ := m.Service.Active()
	out := MajorExtensions{Major: major, Extensions: []ExtensionState{}, CustomDir: CustomDir(m.root(), major),
		Custom: CustomExtensions(m.root(), major, m.installedByMajor(active)[major])}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range Extensions {
		s := ExtensionState{Extension: e, Busy: m.busy[extensionKey(major, e.Name)]}
		installed := InstalledExtension(m.root(), e.Name, major)
		offer, ok := m.extensionOffer(manifest, e.Name, major)
		s.Supported = ok || installed != ""
		if ok {
			s.Latest, s.Size, s.Prerelease = offer.Version, offer.Size, offer.Prerelease
		}
		if installed != "" {
			s.Installed = extensionVersion(installed)
			s.Yanked = m.yankedExtension(manifest, e.Name, major, installed)
			s.Update = ok && offer.Installable() && newerExtension(ID(offer.Version, offer.Build), installed)
		}
		if newest := m.newestExtension(manifest, e.Name); ok && newest != "" && binaries.CompareVersions(newest, offer.Version) > 0 {
			s.Discontinued = true
		}
		out.Extensions = append(out.Extensions, s)
	}
	return out, nil
}

func (m *Manager) yankedExtension(manifest *binaries.Manifest, name, major, id string) string {
	version, build, _ := parseExtensionID(id)
	for _, v := range manifest.Versions(ExtensionPackage(name, major), m.platform) {
		if v.Version == version {
			return v.YankReason(build)
		}
	}
	return ""
}

func extensionKey(major, name string) string { return major + "/" + name }

// beginExtension claims the operation lock for an extension of major. Extension operations queue:
// one clicked while another runs waits for it ("waiting"), so several can be installed at once.
// A second operation on the same extension fails with ErrBusy.
func (m *Manager) beginExtension(ctx context.Context, major, name, what string) (Extension, func(), error) {
	e, ok := FindExtension(name)
	if !ok {
		return Extension{}, nil, fmt.Errorf("%w %q", ErrUnknownExtension, name)
	}
	if !majorPattern.MatchString(major) {
		return Extension{}, nil, fmt.Errorf("%w %q: give a major version like %s", ErrInvalidMajor, major, DefaultMajor)
	}
	key := extensionKey(major, name)
	m.mu.Lock()
	if m.busy[key] != "" {
		m.mu.Unlock()
		return Extension{}, nil, ErrBusy
	}
	if m.busy == nil {
		m.busy = map[string]string{}
	}
	m.busy[key] = "waiting" // marked here, so a second click on it is refused right away
	m.mu.Unlock()
	done, err := m.claim(ctx, key, what, true)
	return e, done, err
}

// InstallExtension installs the newest build of an extension for major, or updates to it, and
// makes it available to that major's server.
func (m *Manager) InstallExtension(ctx context.Context, major, name string) error {
	e, done, err := m.beginExtension(ctx, major, name, "installing")
	if err != nil {
		return err
	}
	defer done()
	for _, required := range e.Requires {
		if InstalledExtension(m.root(), required, major) != "" {
			continue
		}
		r, _ := FindExtension(required)
		if err := m.installExtension(ctx, r, major); err != nil {
			return fmt.Errorf("%s needs %s: %w", e.Title, r.Title, err)
		}
	}
	if err := m.installExtension(ctx, e, major); err != nil {
		return err
	}
	return m.extensionsChanged(ctx, major, e)
}

// UpdateExtension updates an installed extension of major to its newest build.
func (m *Manager) UpdateExtension(ctx context.Context, major, name string) error {
	e, done, err := m.beginExtension(ctx, major, name, "updating")
	if err != nil {
		return err
	}
	defer done()
	if InstalledExtension(m.root(), name, major) == "" {
		return fmt.Errorf("%s for PostgreSQL %s is %w", e.Title, major, ErrNotInstalled)
	}
	if err := m.installExtension(ctx, e, major); err != nil {
		return err
	}
	return m.extensionsChanged(ctx, major, e)
}

// UninstallExtension removes an extension from major. Databases that created it keep it in their
// catalog: its functions fail until it is installed again or dropped.
func (m *Manager) UninstallExtension(ctx context.Context, major, name string) error {
	e, done, err := m.beginExtension(ctx, major, name, "uninstalling")
	if err != nil {
		return err
	}
	defer done()
	if InstalledExtension(m.root(), name, major) == "" {
		return fmt.Errorf("%s for PostgreSQL %s is %w", e.Title, major, ErrNotInstalled)
	}
	for _, other := range Extensions {
		if slices.Contains(other.Requires, name) && InstalledExtension(m.root(), other.Name, major) != "" {
			return fmt.Errorf("%s is %w %s: uninstall %s first", e.Title, ErrExtensionInUse, other.Title, other.Title)
		}
	}
	if err := os.RemoveAll(filepath.Join(ExtensionsRoot(m.root()), name, major)); err != nil {
		return err
	}
	return m.extensionsChanged(ctx, major, e)
}

// installExtension installs the channel's newest build of e for major (when it is newer than the
// installed one) and removes the older builds. The caller holds the lock.
func (m *Manager) installExtension(ctx context.Context, e Extension, major string) error {
	if m.Catalog == nil {
		return errors.New("PostgreSQL extensions aren't published for this platform")
	}
	manifest, err := m.Catalog.Check(ctx)
	if err != nil {
		return fmt.Errorf("look up %s versions: %w", e.Title, err)
	}
	offer, ok := m.extensionOffer(manifest, e.Name, major)
	if !ok {
		return fmt.Errorf("%s is not available for PostgreSQL %s", e.Title, major)
	}
	if !offer.Installable() {
		return fmt.Errorf("%s %s build %d was withdrawn: %s", e.Title, offer.Version, offer.Build, offer.YankReason(offer.Build))
	}
	id := ID(offer.Version, offer.Build)
	if current := InstalledExtension(m.root(), e.Name, major); current != "" && !newerExtension(id, current) {
		return nil
	}

	dir := ExtensionDir(m.root(), e.Name, major, id)
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+id+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	pkg := filepath.Join(staging, "package")
	if err := binaries.Install(ctx, m.Installer.HTTP, offer.Entry, pkg, nil); err != nil {
		return fmt.Errorf("%s %s: %w", e.Title, offer.Version, err)
	}
	if !isDir(filepath.Join(pkg, "extension")) {
		return fmt.Errorf("the %s package has no extension/ directory", e.Title)
	}
	if err := os.Rename(pkg, dir); err != nil {
		return err
	}
	for _, old := range installedExtensionIDs(m.root(), e.Name, major) {
		if old != id {
			_ = os.RemoveAll(ExtensionDir(m.root(), e.Name, major, old))
		}
	}
	return nil
}

// extensionsChanged links major's extensions into its installed server. When e is preloaded and
// major is running, the server restarts to load it, and databases that use it are updated.
func (m *Manager) extensionsChanged(ctx context.Context, major string, e Extension) error {
	active, enabled := m.Service.Active()
	id := m.installedByMajor(active)[major]
	if id == "" {
		return nil // linked when the major is installed
	}
	if _, _, err := Link(m.root(), id); err != nil {
		return err
	}
	if !e.Preload || id != active || !enabled {
		return nil
	}
	// The preload setting is part of the server's command line: a reload restarts it.
	if err := m.Service.Reload(); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		return fmt.Errorf("PostgreSQL %s did not start with %s: %w", major, e.Title, err)
	}
	return m.updateExtensionInDatabases(ctx, major)
}

// updateExtensionInDatabases runs ALTER EXTENSION ... UPDATE for the preloaded extensions of the
// running major, in every database that created them. TimescaleDB loads the library of the
// version a database has: after an update, that older library is gone.
func (m *Manager) updateExtensionInDatabases(ctx context.Context, major string) error {
	if !m.Service.Live() || !m.Service.Running() {
		return nil
	}
	preload := PreloadLibraries(m.root(), major)
	if len(preload) == 0 {
		return nil
	}
	databases, err := Databases(ctx, m.Service.Port())
	if err != nil {
		return err
	}
	var errs []error
	for _, db := range databases {
		for _, name := range preload {
			e, _ := FindExtension(name)
			// It must be the first statement of the session, before the old library is loaded.
			err := Exec(ctx, m.Service.Port(), db, "ALTER EXTENSION "+quoteIdent(e.Creates)+" UPDATE")
			var pgErr *Error
			if errors.As(err, &pgErr) && pgErr.Code == sqlstateUndefinedObject {
				continue // this database doesn't use it
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("update %s in database %s: %w", e.Title, db, err))
			}
		}
	}
	return errors.Join(errs...)
}

// installExtensions installs extensions for a newly installed major; the caller holds the lock.
// Extensions without a build for major are skipped.
func (m *Manager) installExtensions(ctx context.Context, major string, names []string) error {
	var errs []error
	// What they require goes first.
	var ordered []string
	for _, name := range names {
		if e, ok := FindExtension(name); ok {
			for _, required := range e.Requires {
				if !slices.Contains(ordered, required) {
					ordered = append(ordered, required)
				}
			}
		}
		if !slices.Contains(ordered, name) {
			ordered = append(ordered, name)
		}
	}
	for _, name := range ordered {
		e, ok := FindExtension(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w %q", ErrUnknownExtension, name))
			continue
		}
		if _, ok := m.extensionOffer(m.manifest(), name, major); !ok {
			continue
		}
		m.setBusy(extensionKey(major, name), "installing")
		err := m.installExtension(ctx, e, major)
		m.clearBusy(extensionKey(major, name))
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// replaceYankedExtensions updates every installed extension whose build was withdrawn, when a
// good one exists.
func (m *Manager) replaceYankedExtensions() {
	ids, _ := InstalledIDs(m.root())
	seen := map[string]bool{}
	for _, id := range ids {
		major := MajorOf(id)
		if seen[major] {
			continue
		}
		seen[major] = true
		state, err := m.Extensions(major)
		if err != nil {
			continue
		}
		for _, e := range state.Extensions {
			if e.Yanked != "" && e.Update && e.Busy == "" {
				go func(major, name string) { _ = m.UpdateExtension(context.Background(), major, name) }(major, e.Name)
			}
		}
	}
}
