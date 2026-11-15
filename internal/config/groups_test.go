package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func groupsConfig() *Config {
	cfg := Default()
	cfg.Upsert(Site{Name: "api", Path: "/code/api", Group: "Work"})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Group: "Work"})
	cfg.Upsert(Site{Name: "blog", Path: "/code/blog", Group: "Personal"})
	cfg.Upsert(Site{Name: "scratch", Path: "/code/scratch"})
	return cfg
}

func TestNormalizeGroupTidiesTheName(t *testing.T) {
	cfg := groupsConfig()

	for in, want := range map[string]string{
		"Clients":                           "Clients",
		"  Clients  ":                       "Clients",
		"Big   Client":                      "Big Client",
		"tabs\tand\nlines":                  "tabs and lines",
		"":                                  "",
		"   ":                               "",
		"work":                              "Work", // an existing group keeps its spelling
		"WORK":                              "Work",
		" personal ":                        "Personal",
		"Ünïcode groups ✓":                  "Ünïcode groups ✓",
		strings.Repeat("a", MaxGroupLength): strings.Repeat("a", MaxGroupLength),
	} {
		got, err := cfg.NormalizeGroup(in)
		if err != nil || got != want {
			t.Errorf("NormalizeGroup(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeGroupRejectsWhatCannotBeAName(t *testing.T) {
	cfg := groupsConfig()

	for _, in := range []string{
		strings.Repeat("a", MaxGroupLength+1),
		"bell\x07",
		"nul\x00byte",
	} {
		if got, err := cfg.NormalizeGroup(in); err == nil {
			t.Errorf("NormalizeGroup(%q) = %q; want an error", in, got)
		}
	}
}

func TestSetGroupAssignsAndClears(t *testing.T) {
	cfg := groupsConfig()
	site, _ := cfg.FindSite("scratch")

	if err := cfg.SetGroup(&site, "work"); err != nil || site.Group != "Work" {
		t.Fatalf("SetGroup(work) = %v, group %q; want Work", err, site.Group)
	}
	if err := cfg.SetGroup(&site, ""); err != nil || site.Group != "" {
		t.Fatalf("SetGroup(\"\") = %v, group %q; want it cleared", err, site.Group)
	}
	if err := cfg.SetGroup(&site, "bell\x07"); err == nil {
		t.Error("SetGroup accepted a name with a control character")
	}
}

func TestGroupsListsEachGroupOnceInOrder(t *testing.T) {
	cfg := groupsConfig()

	want := []string{"Personal", "Work"}
	if got := cfg.Groups(); !reflect.DeepEqual(got, want) {
		t.Errorf("Groups() = %v; want %v", got, want)
	}
	if got := Default().Groups(); len(got) != 0 {
		t.Errorf("Groups() of a config without groups = %v; want none", got)
	}
}

func TestSitesInGroupSortedByName(t *testing.T) {
	cfg := groupsConfig()
	names := func(sites []Site) []string {
		out := []string{}
		for _, s := range sites {
			out = append(out, s.Name)
		}
		return out
	}

	if got := names(cfg.SitesInGroup("work")); !reflect.DeepEqual(got, []string{"api", "shop"}) {
		t.Errorf("SitesInGroup(work) = %v", got)
	}
	if got := names(cfg.SitesInGroup("")); !reflect.DeepEqual(got, []string{"scratch"}) {
		t.Errorf("SitesInGroup(\"\") = %v; want the ungrouped sites", got)
	}
	if got := cfg.SitesInGroup("nope"); len(got) != 0 {
		t.Errorf("SitesInGroup(nope) = %v; want none", got)
	}
}

func TestGroupSurvivesSaveAndLoad(t *testing.T) {
	root := t.TempDir()
	if err := groupsConfig().Save(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if site, _ := cfg.FindSite("shop"); site.Group != "Work" {
		t.Errorf("group after reload = %q; want Work", site.Group)
	}
}

func TestAnUngroupedSiteStoresNoGroupKey(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	cfg.Upsert(Site{Name: "solo", Path: "/code/solo"})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file(root))
	if err != nil {
		t.Fatal(err)
	}
	if raw := string(data); strings.Contains(raw, `"group"`) {
		t.Errorf("config of a site without a group mentions it:\n%s", raw)
	}
}
