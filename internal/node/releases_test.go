package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func fakeReleases(t *testing.T) *Installer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dist/index.json":
			_, _ = w.Write([]byte(`[
				{"version":"v25.1.0","lts":false},
				{"version":"v24.11.0","lts":"Krypton"},
				{"version":"v24.10.0","lts":false},
				{"version":"v23.11.1","lts":false},
				{"version":"v22.21.0","lts":"Jod"},
				{"version":"v20.19.5","lts":"Iron"},
				{"version":"v18.20.8","lts":"Hydrogen"},
				{"version":"v14.21.3","lts":"Fermium"}
			]`))
		case "/schedule.json":
			_, _ = w.Write([]byte(`{
				"v25": {"start":"2025-10-15","maintenance":"2026-04-01","end":"2026-06-01"},
				"v24": {"start":"2025-05-06","lts":"2025-10-28","maintenance":"2026-10-20","end":"2028-04-30"},
				"v23": {"start":"2024-10-16","maintenance":"2025-04-01","end":"2025-06-01"},
				"v22": {"start":"2024-04-24","lts":"2024-10-29","maintenance":"2025-10-21","end":"2027-04-30"},
				"v20": {"start":"2023-04-18","lts":"2023-10-24","maintenance":"2024-10-22","end":"2026-04-30"},
				"v18": {"start":"2022-04-19","lts":"2022-10-25","maintenance":"2023-10-18","end":"2025-04-30"}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Installer{BaseURL: srv.URL + "/dist", ScheduleURL: srv.URL + "/schedule.json"}
}

func TestLinesAreNewestReleasePerMajorWithSupport(t *testing.T) {
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	lines, err := fakeReleases(t).Lines(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Line{}
	var majors []string
	for _, l := range lines {
		got[l.Major] = l
		majors = append(majors, l.Major)
	}
	// 25 and 23 are odd lines past their end; 14 is older than MinimumMajor.
	if want := []string{"24", "22", "20", "18"}; !slices.Equal(majors, want) {
		t.Fatalf("majors %v, want %v", majors, want)
	}
	if l := got["24"]; l.Latest != "24.11.0" || l.Codename != "Krypton" || l.Support != LTS || l.EndOfLife != "2028-04-30" {
		t.Errorf("24: %+v", l)
	}
	if got["22"].Support != Maintenance || got["20"].Support != EndOfLife || got["18"].Support != EndOfLife {
		t.Errorf("support: 22=%s 20=%s 18=%s", got["22"].Support, got["20"].Support, got["18"].Support)
	}

	// Earlier, 25 was the current line.
	lines, _ = fakeReleases(t).Lines(context.Background(), time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC))
	if lines[0].Major != "25" || lines[0].Support != Current {
		t.Errorf("25 in November 2025: %+v", lines[0])
	}
}

func TestLinesWithoutScheduleStillListReleases(t *testing.T) {
	inst := fakeReleases(t)
	inst.ScheduleURL = inst.BaseURL + "/missing.json"
	lines, err := inst.Lines(context.Background(), time.Now())
	if err != nil || len(lines) == 0 || lines[0].Support != "" {
		t.Fatalf("lines %+v, err %v", lines, err)
	}
}

func TestUninstallAndRemoveOlder(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"22.20.0", "22.21.0", "20.19.5"} {
		if err := os.MkdirAll(filepath.Join(Dir(root), v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	RemoveOlder(root, "22.21.0")
	if got, _ := Installed(root); !slices.Equal(got, []string{"22.21.0", "20.19.5"}) {
		t.Fatalf("after RemoveOlder: %v", got)
	}
	if err := Uninstall(root, "22"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Installed(root); !slices.Equal(got, []string{"20.19.5"}) {
		t.Fatalf("after Uninstall: %v", got)
	}
}
