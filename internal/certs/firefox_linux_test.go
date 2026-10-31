package certs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An administrator's policies survive installing and removing Flock's, and a policy file Flock
// created goes with it.
func TestFirefoxPolicyKeepsOtherPolicies(t *testing.T) {
	old := FirefoxPolicyDir
	FirefoxPolicyDir = filepath.Join(t.TempDir(), "firefox", "policies")
	t.Cleanup(func() { FirefoxPolicyDir = old })

	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// No policy yet: Flock's file is created, and removed again.
	for range 2 {
		if err := InstallFirefoxPolicy("ada", ca); err != nil {
			t.Fatal(err)
		}
	}
	if !FirefoxPolicyInstalled("ada", ca) {
		t.Fatal("not installed")
	}
	if err := RemoveFirefoxPolicy("ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(FirefoxPolicyDir)); !os.IsNotExist(err) {
		t.Fatalf("policy dir left behind: %v", err)
	}

	// An administrator's policy, with a certificate of their own.
	admin := `{"policies":{"DisableTelemetry":true,"Certificates":{"Install":["/etc/corp.crt"]}}}`
	if err := os.MkdirAll(FirefoxPolicyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(FirefoxPolicyDir, "policies.json")
	if err := os.WriteFile(file, []byte(admin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := InstallFirefoxPolicy("ada", ca); err != nil {
		t.Fatal(err)
	}
	if got := installList(mustPolicies(t)); !reflect.DeepEqual(got, []string{"/etc/corp.crt", firefoxCA("ada")}) {
		t.Fatalf("Install = %v", got)
	}
	if err := RemoveFirefoxPolicy("ada"); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(admin), &want)
	if got := mustPolicies(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("after removal: %v, want %v", got, want)
	}
}

func mustPolicies(t *testing.T) map[string]any {
	t.Helper()
	doc, err := readPolicies()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
