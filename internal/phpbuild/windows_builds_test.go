package phpbuild

import "testing"

func TestParseWindowsReleasesPicksNTSx64(t *testing.T) {
	body := []byte(`{
	  "7.4": {"version": "7.4.33", "nts-vc15-x64": {"zip": {"path": "php-7.4.33-nts-Win32-vc15-x64.zip", "sha256": "AA"}}},
	  "8.4": {"version": "8.4.26",
	          "ts-vs17-x64": {"zip": {"path": "php-8.4.26-Win32-vs17-x64.zip", "sha256": "bb"}},
	          "nts-vs17-x86": {"zip": {"path": "php-8.4.26-nts-Win32-vs17-x86.zip", "sha256": "cc"}},
	          "nts-vs17-x64": {"zip": {"path": "php-8.4.26-nts-Win32-vs17-x64.zip", "size": "33.48MB", "sha256": "da68"}},
	          "source": {"path": "php-8.4.26-src.zip"}}
	}`)

	builds, err := parseWindowsReleases(body, "https://w/releases")
	if err != nil {
		t.Fatal(err)
	}
	want := WindowsBuild{Version: "8.4.26", URL: "https://w/releases/php-8.4.26-nts-Win32-vs17-x64.zip", SHA256: "da68", VS: "vs17"}
	if builds["8.4"] != want {
		t.Errorf("8.4: got %+v", builds["8.4"])
	}
	if b := builds["7.4"]; b.VS != "vc15" || b.SHA256 != "aa" {
		t.Errorf("7.4: got %+v", b)
	}
}

func TestParseWindowsArchiveKeepsNewestPerMinor(t *testing.T) {
	listing := `<a href="/downloads/releases/archives/php-8.1.9-nts-Win32-vs16-x64.zip">x</a>
<a href="/downloads/releases/archives/php-8.1.31-nts-Win32-vs16-x64.zip">x</a>
<a href="/downloads/releases/archives/php-8.1.32-Win32-vs16-x64.zip">ts</a>
<a href="/downloads/releases/archives/php-8.1.33-nts-Win32-vs16-x86.zip">x86</a>
<a href="/downloads/releases/archives/php-5.6.40-nts-Win32-VC11-x64.zip">old</a>`

	builds := parseWindowsArchive(listing, "https://w/archives/")
	if len(builds) != 1 || builds["8.1"].Version != "8.1.31" || builds["8.1"].URL != "https://w/archives/php-8.1.31-nts-Win32-vs16-x64.zip" {
		t.Fatalf("got %+v", builds)
	}
}

func TestFindXdebugDLLMatchesPHPAndCompiler(t *testing.T) {
	page := `<a href="/files/php_xdebug-3.5.3-8.4-nts-vs17-x86_64.dll">
<a href="/files/php_xdebug-3.5.3-8.4-ts-vs17-x86_64.dll">
<a href="/files/php_xdebug-3.4.0-8.4-nts-vs17-x86_64.dll">
<a href="/files/php_xdebug-3.2.2-8.2-vs16-nts-x86_64.dll">`

	for minor, want := range map[string]string{
		"8.4": "https://xdebug.org/files/php_xdebug-3.5.3-8.4-nts-vs17-x86_64.dll",
		"8.2": "https://xdebug.org/files/php_xdebug-3.2.2-8.2-vs16-nts-x86_64.dll",
	} {
		vs := map[string]string{"8.4": "vs17", "8.2": "vs16"}[minor]
		if got, err := findXdebugDLL(page, "https://xdebug.org", minor, vs); err != nil || got != want {
			t.Errorf("%s: got %q %v", minor, got, err)
		}
	}
	if _, err := findXdebugDLL(page, "https://xdebug.org", "8.4", "vs16"); err == nil {
		t.Error("a DLL built with another compiler must not match")
	}
}
