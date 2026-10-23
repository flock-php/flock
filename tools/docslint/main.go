// Command docslint checks the documentation in docs/ that flockphp.com shows: that navigation.json
// and the pages agree, that every :::app and :::cli block is closed, that relative links and
// images point at files that exist, and that a screenshot:id image has all of its variants or
// none yet. Run it from the repository root:
//
//	go run ./tools/docslint
//
// CI runs it on pull requests that change docs/.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const dir = "docs"

// The blocks the website's App / CLI switch knows (DocsModes on the website).
var modes = []string{"app", "cli"}

// The website's route only matches slugs like these.
var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

var (
	fence       = regexp.MustCompile("^\\s*(```|~~~)")
	blockOpen   = regexp.MustCompile(`^:::(\w+)\s*$`)
	inlineCode  = regexp.MustCompile("`[^`]*`")
	linkPattern = regexp.MustCompile(`(!?)\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	scheme      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

var imageExtensions = []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp"}

// The variants of each screenshot:id image.
var (
	screenshotPlatforms = []string{"macos", "windows"}
	screenshotThemes    = []string{"light", "dark"}
)

// navigation.json: the sidebar's sections and their pages, in order.
type section struct {
	Section string `json:"section"`
	Pages   []struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"pages"`
}

func main() {
	problems, err := lint(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "docslint:", err)
		os.Exit(1)
	}
	for _, p := range problems {
		fmt.Println(p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "docslint: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
}

// lint returns the problems in the documentation in root, each as "file:line: message".
func lint(root string) ([]string, error) {
	var problems []string
	report := func(file string, line int, format string, args ...any) {
		where := filepath.ToSlash(filepath.Join(root, file))
		if line > 0 {
			where = fmt.Sprintf("%s:%d", where, line)
		}
		problems = append(problems, where+": "+fmt.Sprintf(format, args...))
	}

	pages, err := filepath.Glob(filepath.Join(root, "*.md"))
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages in %s", root)
	}

	listed := map[string]bool{}
	nav, err := os.ReadFile(filepath.Join(root, "navigation.json"))
	if err != nil {
		return nil, err
	}
	var sections []section
	decoder := json.NewDecoder(bytes.NewReader(nav))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sections); err != nil {
		report("navigation.json", 0, "%v", err)
	}
	for _, s := range sections {
		if s.Section == "" {
			report("navigation.json", 0, "a section has no name")
		}
		if len(s.Pages) == 0 {
			report("navigation.json", 0, "section %q has no pages", s.Section)
		}
		for _, p := range s.Pages {
			switch {
			case !slugPattern.MatchString(p.Slug):
				report("navigation.json", 0, "slug %q may only have lowercase letters, digits and dashes", p.Slug)
			case listed[p.Slug]:
				report("navigation.json", 0, "%s is listed more than once", p.Slug)
			case !exists(filepath.Join(root, p.Slug+".md")):
				report("navigation.json", 0, "%s has no %s.md", p.Slug, p.Slug)
			}
			if p.Title == "" {
				report("navigation.json", 0, "%s has no title", p.Slug)
			}
			listed[p.Slug] = true
		}
	}

	for _, page := range pages {
		name := filepath.Base(page)
		if !listed[strings.TrimSuffix(name, ".md")] {
			report(name, 0, "not in navigation.json")
		}
		if err := lintPage(root, name, report); err != nil {
			return nil, err
		}
	}
	return problems, nil
}

func lintPage(root, name string, report func(string, int, string, ...any)) error {
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return err
	}
	defer f.Close()

	// As the website reads them: fences inside code blocks don't count.
	inCode := false
	openMode, openLine := "", 0
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := scanner.Text()
		if fence.MatchString(line) {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}

		if m := blockOpen.FindStringSubmatch(line); m != nil {
			switch {
			case !slices.Contains(modes, m[1]):
				report(name, n, ":::%s isn't a block the website knows (:::%s)", m[1], strings.Join(modes, ", :::"))
			case openMode != "":
				report(name, n, ":::%s inside the :::%s block from line %d; close that one with ::: first", m[1], openMode, openLine)
			default:
				openMode, openLine = m[1], n
			}
			continue
		}
		if strings.TrimSpace(line) == ":::" {
			if openMode == "" {
				report(name, n, "::: closes no :::%s block", strings.Join(modes, " or :::"))
			}
			openMode = ""
			continue
		}

		for _, m := range linkPattern.FindAllStringSubmatch(inlineCode.ReplaceAllString(line, ""), -1) {
			lintTarget(root, name, n, m[1] == "!", m[2], report)
		}
	}
	if openMode != "" {
		report(name, openLine, ":::%s is never closed with :::", openMode)
	}
	return scanner.Err()
}

// lintTarget checks a link's or an image's target: a full URL, an anchor on the page, or a file
// in root that exists (another page, or an image in images/).
func lintTarget(root, name string, line int, image bool, target string, report func(string, int, string, ...any)) {
	if strings.HasPrefix(target, "#") {
		return
	}
	if id, ok := strings.CutPrefix(target, "screenshot:"); ok {
		lintScreenshot(root, name, line, image, id, report)
		return
	}
	if s := scheme.FindString(target); s != "" {
		if !slices.Contains([]string{"http:", "https:", "mailto:"}, strings.ToLower(s)) {
			report(name, line, "%s: link to a file in docs/ by its path, or to a full URL", target)
		}
		return
	}
	if strings.HasPrefix(target, "/") {
		report(name, line, "%s: link to another page by its file (e.g. quick-start.md), and to the rest of the website with a full URL", target)
		return
	}

	file, _, _ := strings.Cut(target, "#")
	file, _, _ = strings.Cut(file, "?")
	clean := path.Clean(file)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		report(name, line, "%s points outside docs/", target)
		return
	}
	if !exists(filepath.Join(root, filepath.FromSlash(clean))) {
		report(name, line, "%s doesn't exist", target)
		return
	}
	if image && !slices.Contains(imageExtensions, strings.ToLower(path.Ext(clean))) {
		report(name, line, "%s isn't an image (%s)", target, strings.Join(imageExtensions, ", "))
	}
}

// lintScreenshot checks a screenshot:id image. Its files are images/{id}-{platform}-{theme}.png,
// as tools/screenshots' FlockScreenshots renders them. Until they're added, the website shows a
// placeholder; once any is there, all of them must be.
func lintScreenshot(root, name string, line int, image bool, id string, report func(string, int, string, ...any)) {
	if !image {
		report(name, line, "screenshot:%s is for images: ![Alt](screenshot:%s)", id, id)
		return
	}
	if !slugPattern.MatchString(id) {
		report(name, line, "screenshot:%s: an id may only have lowercase letters, digits and dashes", id)
		return
	}
	var missing []string
	for _, platform := range screenshotPlatforms {
		for _, theme := range screenshotThemes {
			file := fmt.Sprintf("images/%s-%s-%s.png", id, platform, theme)
			if !exists(filepath.Join(root, filepath.FromSlash(file))) {
				missing = append(missing, file)
			}
		}
	}
	if len(missing) > 0 && len(missing) < len(screenshotPlatforms)*len(screenshotThemes) {
		report(name, line, "screenshot:%s is missing %s", id, strings.Join(missing, ", "))
	}
}

func exists(file string) bool {
	info, err := os.Stat(file)
	return err == nil && !info.IsDir()
}
