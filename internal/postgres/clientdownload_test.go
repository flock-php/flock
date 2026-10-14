package postgres

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, names []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range names {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(name))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestExtractClientKeepsOnlyTheClientToolsAndTheirDLLs(t *testing.T) {
	dir := t.TempDir()
	names := []string{"pgsql/", "pgsql/bin/", "pgsql/bin/postgres.exe", "pgsql/bin/initdb.exe", "pgsql/bin/icuuc77.dll",
		"pgsql/pgAdmin 4/python/python.exe", "pgsql/server_license.txt", "pgsql/commandlinetools_3rd_party_licenses.txt"}
	for _, tool := range ClientTools {
		names = append(names, "pgsql/bin/"+tool+".exe")
	}
	for _, dll := range clientDLLs {
		names = append(names, "pgsql/bin/"+dll)
	}
	archive := filepath.Join(dir, "pg.zip")
	writeZip(t, archive, names)

	out := filepath.Join(dir, "out")
	if err := extractClient(archive, out); err != nil {
		t.Fatal(err)
	}
	bin, _ := os.ReadDir(filepath.Join(out, "bin"))
	if len(bin) != len(ClientTools)+len(clientDLLs) {
		t.Fatalf("bin/ has %d files, want %d", len(bin), len(ClientTools)+len(clientDLLs))
	}
	for _, unwanted := range []string{"bin/postgres.exe", "bin/icuuc77.dll", "pgAdmin 4"} {
		if _, err := os.Stat(filepath.Join(out, unwanted)); err == nil {
			t.Errorf("%s extracted", unwanted)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(out, "bin", "psql.exe")); string(data) != "pgsql/bin/psql.exe" {
		t.Fatalf("psql.exe holds %q", data)
	}
	if _, err := os.Stat(filepath.Join(out, "server_license.txt")); err != nil {
		t.Fatal("license not kept")
	}
}

func TestExtractClientFailsWhenAToolIsMissing(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pg.zip")
	writeZip(t, archive, []string{"pgsql/bin/psql.exe"})
	if err := extractClient(archive, filepath.Join(dir, "out")); err == nil || !strings.Contains(err.Error(), "not in archive") {
		t.Fatalf("want a missing-file error, got %v", err)
	}
}

func TestPrebuiltClientOnlyOnWindowsX64(t *testing.T) {
	if b, ok := PrebuiltClient("windows", "amd64"); !ok || len(b.SHA256) != 64 || !strings.Contains(b.URL, b.Version) {
		t.Fatalf("windows/amd64: %+v %v", b, ok)
	}
	if _, ok := PrebuiltClient("darwin", "arm64"); ok {
		t.Fatal("macOS builds the client tools from source")
	}
}
