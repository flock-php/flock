package binaries

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signed(t *testing.T, priv ed25519.PrivateKey, body string) ([]byte, []byte) {
	t.Helper()
	data := []byte(body)
	return data, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)) + "\n")
}

func TestVerifyChecksSignatureExpiryAndSerial(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := Source{Key: pub, SerialFile: filepath.Join(t.TempDir(), "serial")}
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

	manifest := func(serial string) string {
		return `{"schema":1,"serial":` + serial + `,"expires":"2026-11-01T00:00:00Z","packages":{"frankenphp":{"1.13.0":{"php8.4-macos-arm64":{"build":2,"url":"https://example.test/f.tar.gz","sha256":"ab","size":1}}}}}`
	}

	data, sig := signed(t, priv, manifest("100"))
	m, err := s.verify(data, sig, now)
	if err != nil {
		t.Fatal(err)
	}
	if e := m.Packages["frankenphp"]["1.13.0"]["php8.4-macos-arm64"]; e.Build != 2 || e.URL == "" {
		t.Errorf("entry: %+v", e)
	}

	if _, err := s.verify(append(data, ' '), sig, now); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("tampered manifest: %v", err)
	}
	if _, err := s.verify(data, sig, now.AddDate(0, 1, 0)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired manifest: %v", err)
	}
	old, oldSig := signed(t, priv, manifest("99"))
	if _, err := s.verify(old, oldSig, now); err == nil || !strings.Contains(err.Error(), "older") {
		t.Errorf("replayed manifest: %v", err)
	}
	if _, err := s.verify(data, sig, now); err != nil {
		t.Errorf("the same manifest again: %v", err)
	}
}

func TestEmbeddedKeyAndPlatforms(t *testing.T) {
	if len(PublicKey()) != ed25519.PublicKeySize {
		t.Fatal("embedded key")
	}
	for goos, want := range map[string]string{"darwin": "macos-arm64", "windows": "windows-x64", "linux": ""} {
		arch := "arm64"
		if goos == "windows" {
			arch = "amd64"
		}
		if got := platform(goos, arch); got != want {
			t.Errorf("%s: %q, want %q", goos, got, want)
		}
	}
}
