package certs

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Chrome, Chromium and Firefox on Linux don't use the system trust store: they read NSS
// databases in the user's home. Firefox gets Flock's CA through its enterprise policy (see
// FirefoxPolicy); the Chromium family through its NSS database, which Flock adds the CA to.

// nssNickname is what Flock calls its CA in the databases it adds it to.
const nssNickname = "Flock CA"

// ErrNoCertutil is returned when NSS' certutil, which edits the databases, isn't installed.
var ErrNoCertutil = errors.New("certutil is missing — install libnss3-tools (Debian, Ubuntu) or nss-tools (Fedora)")

// chromeDBs are the Chromium family's NSS databases in home: the shared one (Chrome, Chromium and
// other .deb and .rpm browsers), which is created when missing so a browser installed later finds
// the CA, and those of Snap and Flatpak browsers that exist.
func chromeDBs(home string) []string {
	dbs := []string{filepath.Join(home, ".pki", "nssdb")}
	for _, pattern := range []string{
		filepath.Join(home, "snap", "*", "current", ".pki", "nssdb"),
		filepath.Join(home, ".var", "app", "*", ".pki", "nssdb"),
	} {
		matches, _ := filepath.Glob(pattern)
		dbs = append(dbs, matches...)
	}
	return dbs
}

// firefoxDBs are the profiles of Firefox (.deb/.rpm, Snap and Flatpak) in home. The policy puts
// the CA in each when Firefox starts; uninstalling takes it out again.
func firefoxDBs(home string) []string {
	var dbs []string
	for _, pattern := range []string{
		filepath.Join(home, ".mozilla", "firefox", "*", "cert9.db"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "*", "cert9.db"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox", "*", "cert9.db"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			dbs = append(dbs, filepath.Dir(m))
		}
	}
	return dbs
}

// TrustInBrowsers adds the CA at caPath to the Chromium family's databases where it is missing.
func TrustInBrowsers(caPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("certutil"); err != nil {
		return ErrNoCertutil
	}
	fingerprint, err := FileFingerprint(caPath)
	if err != nil {
		return err
	}
	var errs []error
	for _, db := range chromeDBs(home) {
		if len(nssNicknames(db, fingerprint)) > 0 {
			continue
		}
		if !fileExists(filepath.Join(db, "cert9.db")) {
			if err := os.MkdirAll(db, 0o700); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := certutil("-N", "-d", "sql:"+db, "--empty-password"); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		errs = append(errs, certutil("-A", "-d", "sql:"+db, "-t", "C,,", "-n", nssNickname, "-i", caPath))
	}
	return errors.Join(errs...)
}

// TrustedInBrowsers reports whether every Chromium-family database has the CA at caPath.
func TrustedInBrowsers(caPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	fingerprint, err := FileFingerprint(caPath)
	if err != nil {
		return false
	}
	for _, db := range chromeDBs(home) {
		if len(nssNicknames(db, fingerprint)) == 0 {
			return false
		}
	}
	return true
}

// UntrustInBrowsers removes the CA at caPath from every browser database, Firefox's included.
func UntrustInBrowsers(caPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	fingerprint, err := FileFingerprint(caPath)
	if err != nil {
		return nil // no CA, nothing trusted
	}
	if _, err := exec.LookPath("certutil"); err != nil {
		return nil // nothing can have added it
	}
	var errs []error
	for _, db := range append(chromeDBs(home), firefoxDBs(home)...) {
		for _, nick := range nssNicknames(db, fingerprint) {
			errs = append(errs, certutil("-D", "-d", "sql:"+db, "-n", nick))
		}
	}
	return errors.Join(errs...)
}

// trustFlags are certutil's trust attributes for SSL, S/MIME and code signing.
var trustFlags = regexp.MustCompile(`^[a-zA-Z]*,[a-zA-Z]*,[a-zA-Z]*$`)

// nssNicknames are the nicknames under which the database at db holds the certificate with the
// given SHA-1 fingerprint (Firefox names policy imports after the subject, Flock uses its own).
func nssNicknames(db, fingerprint string) []string {
	if !fileExists(filepath.Join(db, "cert9.db")) {
		return nil
	}
	out, err := exec.Command("certutil", "-L", "-d", "sql:"+db).Output()
	if err != nil {
		return nil
	}
	var nicks []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		// "<nickname> <trust flags>": the flags (e.g. "CT,C,") take no spaces, and a long nickname
		// leaves just one before them.
		line := strings.TrimRight(sc.Text(), " ")
		i := strings.LastIndex(line, " ")
		if i < 0 || !trustFlags.MatchString(line[i+1:]) {
			continue
		}
		nick := strings.TrimSpace(line[:i])
		if nick == "" {
			continue
		}
		pem, err := exec.Command("certutil", "-L", "-d", "sql:"+db, "-n", nick, "-a").Output()
		if err == nil && pemFingerprint(pem) == fingerprint {
			nicks = append(nicks, nick)
		}
	}
	return nicks
}

// pemFingerprint is the SHA-1 of the first certificate in data, "" if there is none.
func pemFingerprint(data []byte) string {
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	return fmt.Sprintf("%X", sha1.Sum(block.Bytes))
}

func certutil(args ...string) error {
	out, err := exec.Command("certutil", args...).CombinedOutput()
	if err != nil {
		return errors.New("certutil " + strings.Join(args, " ") + ": " + strings.TrimSpace(string(out)))
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
