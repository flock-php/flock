package cli

import (
	"bytes"
	"strings"
	"testing"

	"flock/internal/config"
)

func TestWriteGroupsListsEveryGroupThenUngroupedSites(t *testing.T) {
	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "shop", Path: "/code/shop", Group: "Work", Secured: true})
	cfg.Upsert(config.Site{Name: "api", Path: "/code/api", Group: "Work"})
	cfg.Upsert(config.Site{Name: "blog", Path: "/code/blog", Group: "Personal"})
	cfg.Upsert(config.Site{Name: "scratch", Path: "/code/scratch"})

	var out bytes.Buffer
	writeGroups(&out, cfg, "")

	want := []string{
		"GROUP",
		"Personal  blog     http://blog.test",
		"Work      api      http://api.test",
		"Work      shop     https://shop.test",
		"-         scratch  http://scratch.test",
	}
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got:\n%s", out.String())
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("line %d = %q; want it to start with %q", i, got[i], want[i])
		}
	}
}

func TestWriteGroupsCanShowOneGroup(t *testing.T) {
	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "shop", Path: "/code/shop", Group: "Work"})
	cfg.Upsert(config.Site{Name: "blog", Path: "/code/blog", Group: "Personal"})
	cfg.Upsert(config.Site{Name: "scratch", Path: "/code/scratch"})

	var out bytes.Buffer
	writeGroups(&out, cfg, "Work")

	if s := out.String(); !strings.Contains(s, "shop") || strings.Contains(s, "blog") || strings.Contains(s, "scratch") {
		t.Errorf("got:\n%s", s)
	}
}

func TestSiteCellAddsTheGroupColumnOnlyWhenListed(t *testing.T) {
	for _, tc := range []struct {
		name, group string
		grouped     bool
		want        string
	}{
		{"shop", "Work", false, "shop"},
		{"shop", "", false, "shop"},
		{"shop", "Work", true, "shop\tWork"},
		{"scratch", "", true, "scratch\t-"},
	} {
		if got := siteCell(tc.name, tc.group, tc.grouped); got != tc.want {
			t.Errorf("siteCell(%q, %q, %v) = %q; want %q", tc.name, tc.group, tc.grouped, got, tc.want)
		}
	}
}

func TestProxyRowsKeepTheirColumnsWhenSitesAreGrouped(t *testing.T) {
	rows := []proxyRow{{name: "minio", url: "http://minio.test", upstream: "http://127.0.0.1:9000"}}
	for grouped, want := range map[bool]string{
		false: "minio\tproxy\thttp://minio.test\t-\thttp://127.0.0.1:9000\n",
		true:  "minio\t-\tproxy\thttp://minio.test\t-\thttp://127.0.0.1:9000\n",
	} {
		var out bytes.Buffer
		printProxyRows(&out, rows, grouped)
		if out.String() != want {
			t.Errorf("grouped=%v: got %q; want %q", grouped, out.String(), want)
		}
	}
}
