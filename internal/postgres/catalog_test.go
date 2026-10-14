package postgres

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const sampleMetadata = `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>io.zonky.test.postgres</groupId>
  <artifactId>embedded-postgres-binaries-darwin-arm64v8</artifactId>
  <versioning>
    <latest>18.6.0</latest>
    <versions>
      <version>10.20.0</version>
      <version>13.6.0</version>
      <version>14.9.0</version>
      <version>14.10.0</version>
      <version>14.10.1</version>
      <version>16.15.0</version>
      <version>17.11.0</version>
      <version>17.9.0</version>
      <version>18.4.0</version>
      <version>18.6.0</version>
      <version>18.7.0-beta</version>
    </versions>
  </versioning>
</metadata>`

func TestParseMetadataPicksNewestPerOfferedMajor(t *testing.T) {
	got, err := ParseMetadata([]byte(sampleMetadata))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"14": "14.10.1", "16": "16.15.0", "17": "17.11.0", "18": "18.6.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("major %s: got %q, want %q (all: %v)", k, got[k], v, got)
		}
	}

	if _, err := ParseMetadata([]byte("<metadata/>")); err == nil {
		t.Fatal("metadata without versions must fail")
	}
	if _, err := ParseMetadata([]byte("not xml")); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestVersionHelpers(t *testing.T) {
	if MajorOf("18.6.0") != "18" || MajorOf("18") != "" || MajorOf("18.6") != "" || MajorOf("data") != "" {
		t.Fatal("MajorOf")
	}
	if !Newer("18.10.0", "18.9.0") || Newer("18.6.0", "18.6.0") || Newer("17.11.0", "18.0.0") || !Newer("14.10.1", "14.10.0") {
		t.Fatal("Newer")
	}
	if got := MetadataURL(DefaultBaseURL+"/", "windows-amd64"); got != "https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-windows-amd64/maven-metadata.xml" {
		t.Fatal(got)
	}
	for _, m := range Majors {
		if EndOfLife[m] == "" {
			t.Fatalf("no end of life for %s", m)
		}
	}
}

func TestCatalogCachesAndKeepsVersionsOnFailure(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(sampleMetadata))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "cache", "postgres-versions.json")
	c := &Catalog{Path: path, URL: srv.URL, Client: srv.Client()}
	if !c.Stale() {
		t.Fatal("a never checked catalog is stale")
	}
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Stale() {
		t.Fatal("just checked")
	}

	fail.Store(true)
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	latest, checkedAt, checkErr := c.Latest()
	if latest["18"] != "18.6.0" || checkErr == "" || checkedAt.IsZero() {
		t.Fatalf("got %v %v %q", latest, checkedAt, checkErr)
	}

	// A new catalog (daemon restart) reads the cache.
	again := &Catalog{Path: path, URL: srv.URL}
	if latest, _, _ := again.Latest(); latest["17"] != "17.11.0" {
		t.Fatalf("cache not read: %v", latest)
	}

	old := CheckInterval
	CheckInterval = time.Nanosecond
	defer func() { CheckInterval = old }()
	if !again.Stale() {
		t.Fatal("an old catalog is stale")
	}
}
