package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/binaries/binariestest"
	"flock/internal/catalog"
	"flock/internal/postgres"
)

type stubPGService struct{}

func (stubPGService) Active() (string, bool)   { return "", true }
func (stubPGService) Apply(string, bool) error { return nil }
func (stubPGService) Running() bool            { return false }
func (stubPGService) Port() int                { return 5432 }
func (stubPGService) Live() bool               { return false }

func pgBackend(t *testing.T) *fakeBackend {
	t.Helper()
	srv := binariestest.New(t)
	pkg := binariestest.Zip(t, map[string]string{"bin/postgres": "server"})
	srv.Publish(postgres.Package, "17.6", 1, pkg, nil)
	srv.Publish(postgres.Package, "18.6", 1, pkg, nil)
	root := t.TempDir()
	return &fakeBackend{root: root, pg: postgres.NewManager(root, srv.Catalog(filepath.Join(root, "cache")), stubPGService{})}
}

func TestPostgresVersionsEndpoints(t *testing.T) {
	b := pgBackend(t)
	srv := serve(t, b, "")

	resp, body := call(t, "POST", srv.URL+"/v1/postgres/check", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var v struct {
		Active     string     `json:"active"`
		Running    bool       `json:"running"`
		Enabled    bool       `json:"enabled"`
		Port       int        `json:"port"`
		CheckedAt  *time.Time `json:"checked_at"`
		CheckError string     `json:"check_error"`
		Majors     []map[string]any
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if v.Port != 5432 || !v.Enabled || v.CheckedAt == nil || v.CheckError != "" || len(v.Majors) != 2 {
		t.Fatalf("got %s", body)
	}
	first := v.Majors[0]
	for _, key := range []string{"major", "installed", "latest", "update", "size", "prerelease", "yanked", "active", "has_data", "data_size", "eol", "busy"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("missing %s in %v", key, first)
		}
	}
	if first["major"] != "18" || first["latest"] != "18.6" || first["eol"] != "2030-11-14" || v.Majors[1]["major"] != "17" {
		t.Fatalf("got %v", v.Majors)
	}

	if resp, _ := call(t, "GET", srv.URL+"/v1/postgres/versions", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/v1/postgres/versions/13/install", http.StatusBadRequest},
		{"POST", "/v1/postgres/versions/abc/activate", http.StatusBadRequest},
		{"POST", "/v1/postgres/versions/18/update", http.StatusConflict},
		{"POST", "/v1/postgres/versions/18/activate", http.StatusConflict},
		{"DELETE", "/v1/postgres/versions/18?delete_data=1", http.StatusConflict},
	} {
		resp, body := call(t, c.method, srv.URL+c.path, "", "")
		if resp.StatusCode != c.want || !strings.Contains(body, `"error"`) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, resp.StatusCode, body)
		}
	}

	none := serve(t, &fakeBackend{root: t.TempDir()}, "")
	if resp, _ := call(t, "GET", none.URL+"/v1/postgres/versions", "", ""); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("no manager: %d", resp.StatusCode)
	}
}

func TestCatalogEndpoints(t *testing.T) {
	srv0 := binariestest.New(t)
	srv0.Publish("mariadb", "11.8.9", 1, binariestest.Zip(t, map[string]string{"bin/mariadbd": "server", "share/x": "x"}), nil)
	kind := &catalog.Kind{
		ID: "mariadb", Name: "MariaDB", Category: "Database", DefaultPort: 3306,
		Line: func(v string) string { return strings.Join(strings.SplitN(v, ".", 3)[:2], ".") },
		Env:  func(port int) string { return fmt.Sprintf("DB_PORT=%d", port) },
	}
	root := t.TempDir()
	b := &fakeBackend{root: root}
	m := catalog.NewManager(root, []*catalog.Kind{kind}, srv0.Catalog(filepath.Join(root, "cache")), b.Mutate)
	b.catalog = m
	srv := serve(t, b, "")

	resp, body := call(t, "POST", srv.URL+"/v1/catalog/check", "", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"latest":"11.8.9"`) || !strings.Contains(body, `"default_port":3306`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/catalog/mariadb/install", "", `{"line":"10.6"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown line: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/catalog/mariadb/install", "", `{"port":2525}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("the mail catcher's port: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/catalog/memcached/install", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown service: %d", resp.StatusCode)
	}

	resp, body = call(t, "POST", srv.URL+"/v1/catalog/mariadb/install", "", `{"line":"11.8","port":3307}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"version":"11.8.9"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	m.Wait()
	if _, log := call(t, "GET", srv.URL+"/v1/catalog/mariadb/log", "", ""); !strings.Contains(log, "installed MariaDB 11.8.9 on port 3307") {
		t.Fatalf("log: %s", log)
	}
	_, body = call(t, "GET", srv.URL+"/v1/catalog", "", "")
	if !strings.Contains(body, `"installed":"11.8.9"`) || !strings.Contains(body, `"port":3307`) || !strings.Contains(body, `"job":null`) {
		t.Fatalf("got %s", body)
	}

	// Installed, it starts, stops and changes its port like the built-in services.
	if resp, _ := call(t, "POST", srv.URL+"/v1/services/mariadb/stop", "", ""); resp.StatusCode != http.StatusOK || b.config(t).Services.Running("mariadb") {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/services/mariadb", "", `{"port":3308}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("port: %d", resp.StatusCode)
	}
	if x, _ := b.config(t).Services.Extra("mariadb"); x.Port != 3308 || x.Build != 1 {
		t.Fatalf("port not saved: %+v", x)
	}
	if resp, body := call(t, "PATCH", srv.URL+"/v1/services/postgres", "", `{"port":3308}`); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "mariadb") {
		t.Fatalf("PostgreSQL on MariaDB's port: %d %s", resp.StatusCode, body)
	}

	if resp, _ := call(t, "DELETE", srv.URL+"/v1/catalog/mariadb?delete_data=1", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("uninstall: %d", resp.StatusCode)
	}
	if _, ok := b.config(t).Services.Extra("mariadb"); ok {
		t.Fatal("still configured")
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/services/mariadb/start", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("start after uninstall: %d", resp.StatusCode)
	}

	none := serve(t, &fakeBackend{root: t.TempDir()}, "")
	if resp, _ := call(t, "GET", none.URL+"/v1/catalog", "", ""); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("no catalog: %d", resp.StatusCode)
	}
}
