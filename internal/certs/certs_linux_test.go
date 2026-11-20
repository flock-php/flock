package certs

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestFailedCAKeyWritePreservesCA(t *testing.T) {
	dir := t.TempDir()
	first, err := Ensure(dir, "wsl")
	if err != nil {
		t.Fatal(err)
	}
	key := string(readCAFile(t, first.keyPath()))
	cert := string(readCAFile(t, first.CertPath()))
	rotateCAWithFileSizeLimit(t, dir, 64)
	if string(readCAFile(t, first.keyPath())) != key || string(readCAFile(t, first.CertPath())) != cert {
		t.Fatal("failed key write changed the existing CA")
	}
	checkCAFiles(t, dir)
	reused, err := Ensure(dir, "wsl")
	if err != nil {
		t.Fatal(err)
	}
	if reused.Fingerprint() != first.Fingerprint() {
		t.Fatal("existing CA was not reused after failed rotation")
	}
	rotated, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	certPath, _, err := rotated.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, rotated, certPath, "app.test"); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCACertificateWriteRecovers(t *testing.T) {
	dir := t.TempDir()
	first, err := Ensure(dir, "wsl")
	if err != nil {
		t.Fatal(err)
	}
	key := readCAFile(t, first.keyPath())
	cert := readCAFile(t, first.CertPath())
	if len(cert) <= len(key) {
		t.Fatal("certificate must exceed the key size to isolate its write failure")
	}
	rotateCAWithFileSizeLimit(t, dir, len(key))
	if string(readCAFile(t, first.CertPath())) != string(cert) {
		t.Fatal("failed certificate write changed the existing certificate")
	}
	if string(readCAFile(t, first.keyPath())) == string(key) {
		t.Fatal("key replacement did not complete before the certificate write failed")
	}
	checkCAFiles(t, dir)
	if err := (&CA{dir: dir}).load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load mismatch: got %v, want os.ErrNotExist", err)
	}
	recovered, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	certPath, _, err := recovered.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, recovered, certPath, "app.test"); err != nil {
		t.Fatal(err)
	}
}

func rotateCAWithFileSizeLimit(t *testing.T, dir string, limit int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCARotationWriteFailureProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "FLOCK_TEST_CA_DIR="+dir, "FLOCK_TEST_FILE_SIZE_LIMIT="+strconv.Itoa(limit))
	output, err := cmd.CombinedOutput()
	t.Logf("limited rotation: %s", output)
	if err != nil {
		t.Fatalf("limited rotation subprocess: %v", err)
	}
}

func TestCARotationWriteFailureProcess(t *testing.T) {
	dir := os.Getenv("FLOCK_TEST_CA_DIR")
	if dir == "" {
		return
	}
	limit, err := strconv.ParseUint(os.Getenv("FLOCK_TEST_FILE_SIZE_LIMIT"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: limit}); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(filepath.Clean(dir), "test"); !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("rotation error: got %v, want EFBIG", err)
	} else {
		t.Logf("rotation failed as expected: %v", err)
	}
}

func TestFailedSiteCertificateWriteRecovers(t *testing.T) {
	ca, certPath, keyPath := issueTestSiteCert(t)
	key := readCAFile(t, keyPath)
	cert := readCAFile(t, certPath)
	if len(cert) <= len(key) {
		t.Fatal("certificate must exceed the key size to isolate its write failure")
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	issueSiteCertWithFileSizeLimit(t, ca.dir, len(key))
	checkReplacedSiteKey(t, keyPath, key)
	t.Logf("certificate bytes after failed write: %d, original: %d", len(readCAFile(t, certPath)), len(cert))
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		t.Fatal("failed issuance did not leave an invalid TLS keypair")
	} else {
		t.Logf("TLS pair after failed write: %v", err)
	}
	certPath, keyPath, err := ca.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SiteCert retry returned success: %s %s", certPath, keyPath)
	checkSiteTLSCertificate(t, ca, certPath, keyPath)
}

func checkReplacedSiteKey(t *testing.T, path string, before []byte) {
	t.Helper()
	after := readCAFile(t, path)
	if len(after) != len(before) || bytes.Equal(after, before) {
		t.Fatal("replacement key did not complete within the imposed size limit")
	}
	block, _ := pem.Decode(after)
	if block == nil {
		t.Fatal("replacement key is not PEM")
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err != nil {
		t.Fatal(err)
	}
}

func issueSiteCertWithFileSizeLimit(t *testing.T, dir string, limit int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSiteCertificateWriteFailureProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "FLOCK_TEST_SITE_DIR="+dir, "FLOCK_TEST_FILE_SIZE_LIMIT="+strconv.Itoa(limit))
	output, err := cmd.CombinedOutput()
	t.Logf("limited site issuance: %s", output)
	if err != nil {
		t.Fatalf("limited site issuance subprocess: %v", err)
	}
}

func TestSiteCertificateWriteFailureProcess(t *testing.T) {
	dir := os.Getenv("FLOCK_TEST_SITE_DIR")
	if dir == "" {
		return
	}
	ca, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	limit, err := strconv.ParseUint(os.Getenv("FLOCK_TEST_FILE_SIZE_LIMIT"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: limit}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ca.SiteCert("app.test"); !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("site issuance error: got %v, want EFBIG", err)
	} else {
		t.Logf("site issuance failed as expected: %v", err)
	}
}
