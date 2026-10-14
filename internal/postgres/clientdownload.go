package postgres

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"flock/internal/download"
)

// ClientBuild is a pinned archive of prebuilt client tools.
type ClientBuild struct {
	Version string
	URL     string
	SHA256  string
	Size    int64 // bytes, for the download prompt
}

// windowsClient is EDB's official "binaries" zip of PostgreSQL for Windows (the full server, pgAdmin
// and StackBuilder). EDB publishes no checksums, so the hash is pinned here. Only ClientTools and
// the DLLs they load are kept (clientFiles).
var windowsClient = ClientBuild{
	Version: "18.6",
	URL:     "https://get.enterprisedb.com/postgresql/postgresql-18.6-1-windows-x64-binaries.zip",
	SHA256:  "fbe23da234ee31547bf8a36d29dfd81e82b849df2d2b78d2eecb43d360252f8c",
	Size:    328 << 20,
}

// clientDLLs are the libraries ClientTools import from pgsql/bin (libpq with OpenSSL, gettext,
// lz4/zstd/zlib for pg_dump's compression). The MSVC runtime they need as well (vcruntime140.dll)
// comes with the Visual C++ Redistributable, which PHP needs too.
var clientDLLs = []string{
	"libpq.dll", "libssl-3-x64.dll", "libcrypto-3-x64.dll", "libintl-9.dll", "libiconv-2.dll",
	"libwinpthread-1.dll", "liblz4.dll", "libzstd.dll", "zlib1.dll",
}

// clientLicenses go next to bin/: the PostgreSQL license and those of the bundled libraries.
var clientLicenses = []string{"server_license.txt", "commandlinetools_3rd_party_licenses.txt"}

// PrebuiltClient returns the client tools Flock downloads on goos/goarch; there are none for
// macOS, where build/postgres-client/build.sh builds them from source.
func PrebuiltClient(goos, goarch string) (ClientBuild, bool) {
	if goos == "windows" && goarch == "amd64" {
		return windowsClient, true
	}
	return ClientBuild{}, false
}

// DownloadClient installs the client tools of b and returns their version. progress is passed
// to download.File.
func DownloadClient(ctx context.Context, root string, b ClientBuild, client *http.Client, progress func(done, total int64)) (string, error) {
	parent := filepath.Join(root, "services", "postgres-client")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	archive := filepath.Join(tmp, path.Base(b.URL))
	if err := download.File(ctx, client, b.URL, archive, b.SHA256, progress); err != nil {
		return "", err
	}
	dir := filepath.Join(tmp, "out")
	if err := extractClient(archive, dir); err != nil {
		return "", fmt.Errorf("unpack %s: %w", path.Base(b.URL), err)
	}
	// The archive is no longer needed; drop it before InstallClientFrom copies the tools.
	_ = os.Remove(archive)

	version, err := InstallClientFrom(root, dir)
	if err != nil {
		return "", fmt.Errorf("%w (the PostgreSQL tools need the Microsoft Visual C++ Redistributable: https://aka.ms/vs/17/release/vc_redist.x64.exe)", err)
	}
	return version, nil
}

// clientFiles maps the archive entries Flock keeps to their place in the install directory.
func clientFiles() map[string]string {
	files := map[string]string{}
	for _, tool := range ClientTools {
		files["pgsql/bin/"+tool+".exe"] = "bin/" + tool + ".exe"
	}
	for _, dll := range clientDLLs {
		files["pgsql/bin/"+dll] = "bin/" + dll
	}
	for _, license := range clientLicenses {
		files["pgsql/"+license] = license
	}
	return files
}

// extractClient unpacks clientFiles from an EDB binaries zip into dest (bin/ and the licenses).
func extractClient(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	want := clientFiles()
	for _, f := range zr.File {
		rel, ok := want[strings.ToLower(f.Name)]
		if !ok {
			continue
		}
		if err := extractZipFile(f, filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			return err
		}
		delete(want, strings.ToLower(f.Name))
	}
	for name := range want {
		if strings.HasPrefix(name, "pgsql/bin/") {
			return fmt.Errorf("%s not in archive", name)
		}
	}
	return nil
}

func extractZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return writeFile(target, rc, 0o755)
}
