// Command notices writes THIRD_PARTY_NOTICES: the licenses of the third-party code that ships in
// Flock's builds. That is the Go modules linked into flock and flock-helper (for macOS and
// Windows), the PHP packages embedded for the dumps server, and the runtimes the Windows tray app
// bundles. Run it from the repository root after changing dependencies:
//
//	go run ./tools/notices
//
// With -check it only reports whether the file is up to date (CI runs this).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const output = "THIRD_PARTY_NOTICES"

// The builds Flock ships; each links a slightly different set of modules.
var targets = []struct{ goos, goarch string }{{"darwin", "arm64"}, {"windows", "amd64"}}

var programs = []string{"./cmd/flock", "./cmd/flock-helper"}

const phpVendor = "internal/dumps/server/vendor"

// License files at a module's root. NOTICE and PATENTS files must travel with the code too.
var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([-._].*)?$`)

type component struct {
	name, version string
	files         []string // license files, read in order
}

func main() {
	check := flag.Bool("check", false, "report whether "+output+" is up to date instead of writing it")
	flag.Parse()

	var all []component
	goMods, err := goModules()
	if err != nil {
		fail(err)
	}
	phpPkgs, err := phpPackages()
	if err != nil {
		fail(err)
	}
	all = append(goMods, phpPkgs...)

	var missing []string
	for _, c := range all {
		if len(c.files) == 0 {
			missing = append(missing, c.name+" "+c.version)
		}
	}
	if len(missing) > 0 {
		fail(fmt.Errorf("no license file found for:\n  %s", strings.Join(missing, "\n  ")))
	}

	text, err := render(goMods, phpPkgs)
	if err != nil {
		fail(err)
	}
	if *check {
		current, _ := os.ReadFile(output)
		if !bytes.Equal(normalize(current), text) {
			fail(fmt.Errorf("%s is out of date; run `go run ./tools/notices` and commit the result", output))
		}
		return
	}
	if err := os.WriteFile(output, text, 0o644); err != nil {
		fail(err)
	}
}

func goModules() ([]component, error) {
	seen := map[string]component{}
	for _, t := range targets {
		cmd := exec.Command("go", append([]string{"list", "-deps", "-f",
			"{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}"}, programs...)...)
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED=0")
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s/%s: %w", t.goos, t.goarch, err)
		}
		for line := range strings.Lines(string(out)) {
			fields := strings.Split(strings.TrimSpace(line), "\t")
			if len(fields) != 3 {
				continue
			}
			if _, ok := seen[fields[0]]; ok {
				continue
			}
			files, err := licenseFiles(fields[2])
			if err != nil {
				return nil, err
			}
			seen[fields[0]] = component{name: fields[0], version: fields[1], files: files}
		}
	}
	return sorted(seen), nil
}

// phpPackages lists the Composer packages vendored for the dumps server (embedded in flock), plus
// Composer's own autoloader.
func phpPackages() ([]component, error) {
	data, err := os.ReadFile(filepath.Join(phpVendor, "composer", "installed.json"))
	if err != nil {
		return nil, err
	}
	var installed struct {
		Packages []struct{ Name, Version string } `json:"packages"`
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		return nil, err
	}
	found := map[string]component{}
	for _, p := range installed.Packages {
		files, err := licenseFiles(filepath.Join(phpVendor, filepath.FromSlash(p.Name)))
		if err != nil {
			return nil, err
		}
		found[p.Name] = component{name: p.Name, version: p.Version, files: files}
	}
	files, err := licenseFiles(filepath.Join(phpVendor, "composer"))
	if err != nil {
		return nil, err
	}
	found["composer/composer"] = component{name: "composer/composer", version: "(autoloader)", files: files}
	return sorted(found), nil
}

func licenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && licenseFile.MatchString(e.Name()) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

func sorted(m map[string]component) []component {
	list := make([]component, 0, len(m))
	for _, c := range m {
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b component) int { return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)) })
	return list
}

func render(goMods, phpPkgs []component) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header)

	// Identical license texts (Apache-2.0 above all) are written once and referred to after that.
	firstWith := map[[32]byte]string{}
	section := func(title string, list []component) error {
		fmt.Fprintf(&b, "\n%s\n%s\n", title, strings.Repeat("=", len(title)))
		for _, c := range list {
			heading := c.name + " " + c.version
			fmt.Fprintf(&b, "\n%s\n%s\n", heading, strings.Repeat("-", len(heading)))
			for _, f := range c.files {
				data, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				data = normalize(data)
				sum := sha256.Sum256(data)
				if first, ok := firstWith[sum]; ok {
					fmt.Fprintf(&b, "\n%s: the same text as for %s above.\n", filepath.Base(f), first)
					continue
				}
				firstWith[sum] = heading
				fmt.Fprintf(&b, "\n%s:\n\n%s", filepath.Base(f), data)
			}
		}
		return nil
	}
	if err := section("Go modules in the flock and flock-helper programs", goMods); err != nil {
		return nil, err
	}
	if err := section("PHP packages in the dumps server (embedded in flock)", phpPkgs); err != nil {
		return nil, err
	}
	b.WriteString(windows)
	return b.Bytes(), nil
}

// normalize makes the output the same on every OS: LF line endings, no leading blank lines, one
// trailing newline.
func normalize(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return append(bytes.TrimRight(bytes.TrimLeft(data, "\n"), "\n \t"), '\n')
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "notices:", err)
	os.Exit(1)
}

const header = `Third-party notices for Flock
=============================

Flock is Copyright (C) 2026 Guava s.r.o. and licensed under the GNU Affero General Public License,
version 3 only (see LICENSE and NOTICE). It includes the third-party software below, each part
under its own license, reproduced here.

Generated by ` + "`go run ./tools/notices`" + `; do not edit by hand.
`

const windows = `
The Windows app (FlockTray)
===========================

The Windows app bundles these runtimes, each under its own license:

.NET runtime
------------

The MIT License (MIT)

Copyright (c) .NET Foundation and Contributors

All rights reserved.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

The .NET runtime's own third-party notices:
https://github.com/dotnet/runtime/blob/main/THIRD-PARTY-NOTICES.TXT

Microsoft Windows App SDK
-------------------------

Copyright (c) Microsoft Corporation. The Windows App SDK runtime files are not covered by Flock's
license; they are distributed under the Microsoft Software License Terms for the Windows App SDK:
https://www.nuget.org/packages/Microsoft.WindowsAppSDK/#licenseinfo-body-tab
Its own third-party notices: https://github.com/microsoft/WindowsAppSDK/blob/main/NOTICE.txt
`
