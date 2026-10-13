package laravellog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `#0 cut-off trace from a previous entry
[2026-10-13 10:00:00] local.INFO: User logged in {"id":1}
[2026-10-13 10:00:05] local.ERROR: Division by zero {"exception":"[object] (DivisionByZeroError(code: 0): Division by zero at /app/Foo.php:12)"}
[stacktrace]
#0 /app/routes/web.php(5): App\Foo->bar()
#1 {main}
"}
[2026-10-13T10:01:00.123456+02:00] production.warning: Slow query
`

func TestParseSplitsEntriesWithDetails(t *testing.T) {
	entries := Parse(sample)

	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Level != "info" || entries[0].Message != "User logged in" || entries[0].Details != `{"id":1}` {
		t.Fatalf("entry 0: %+v", entries[0])
	}
	if entries[1].Level != "error" || entries[1].Message != "Division by zero" || !strings.Contains(entries[1].Details, "#0 /app/routes/web.php(5)") {
		t.Fatalf("entry 1: %+v", entries[1])
	}
	if entries[2].Env != "production" || entries[2].Level != "warning" || entries[2].Time.IsZero() {
		t.Fatalf("entry 2: %+v", entries[2])
	}
}

func TestReadReturnsNewestFirstAndClearTruncates(t *testing.T) {
	site := t.TempDir()
	logs := filepath.Join(site, "storage", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "laravel.log"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := Read(site, 2)
	if err != nil || len(entries) != 2 || entries[0].Message != "Slow query" {
		t.Fatalf("got %+v %v", entries, err)
	}

	if err := Clear(site); err != nil {
		t.Fatal(err)
	}
	if entries, _ := Read(site, 0); len(entries) != 0 {
		t.Fatalf("not cleared: %+v", entries)
	}
}

func TestReadWithoutLogsIsEmpty(t *testing.T) {
	entries, err := Read(t.TempDir(), 10)
	if err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("got %v %v", entries, err)
	}
}
