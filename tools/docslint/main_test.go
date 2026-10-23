package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const navigation = `[
  {"section": "Getting started", "pages": [
    {"slug": "introduction", "title": "Introduction"},
    {"slug": "sites", "title": "Sites"}
  ]}
]`

const introduction = "# Introduction\n\nStart with [Sites](sites.md#linking), see [below](#more) or [the changelog](https://flockphp.com/changelog).\n\n![The app](images/app.png)\n\n![Not taken yet](screenshot:settings-php)\n\n![Taken](screenshot:panel)\n\n:::app\nClick **Link**.\n:::\n\n:::cli\n```bash\nflock link\n:::\n```\n:::\n\nWrite `[x](nowhere.md)` for a link.\n"

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func valid(t *testing.T) string {
	root := t.TempDir()
	write(t, root, map[string]string{
		"navigation.json":                navigation,
		"introduction.md":                introduction,
		"sites.md":                       "# Sites\n",
		"images/app.png":                 "png",
		"images/notes.txt":               "",
		"images/panel-macos-light.png":   "png",
		"images/panel-macos-dark.png":    "png",
		"images/panel-windows-light.png": "png",
		"images/panel-windows-dark.png":  "png",
	})
	return root
}

func TestValidDocs(t *testing.T) {
	problems, err := lint(valid(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Errorf("problems in valid docs:\n%s", strings.Join(problems, "\n"))
	}
}

func TestProblems(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"page not in navigation", map[string]string{"extra.md": "# Extra\n"}, "extra.md: not in navigation.json"},
		{"navigation without page", map[string]string{"navigation.json": `[{"section": "S", "pages": [{"slug": "introduction", "title": "I"}, {"slug": "sites", "title": "S"}, {"slug": "gone", "title": "Gone"}]}]`}, "gone has no gone.md"},
		{"duplicate slug", map[string]string{"navigation.json": `[{"section": "S", "pages": [{"slug": "introduction", "title": "I"}, {"slug": "sites", "title": "S"}, {"slug": "sites", "title": "S"}]}]`}, "sites is listed more than once"},
		{"bad slug", map[string]string{"navigation.json": `[{"section": "S", "pages": [{"slug": "introduction", "title": "I"}, {"slug": "sites", "title": "S"}, {"slug": "Sites_2", "title": "S"}]}]`}, `slug "Sites_2"`},
		{"unknown field", map[string]string{"navigation.json": `[{"section": "S", "pages": [{"slug": "introduction", "name": "I"}]}]`}, `unknown field "name"`},
		{"unclosed block", map[string]string{"sites.md": "# Sites\n\n:::app\nClick.\n"}, "sites.md:3: :::app is never closed"},
		{"nested block", map[string]string{"sites.md": ":::app\n:::cli\n:::\n"}, "sites.md:2: :::cli inside the :::app block from line 1"},
		{"unknown block", map[string]string{"sites.md": ":::warning\n:::\n"}, "sites.md:1: :::warning isn't a block"},
		{"stray close", map[string]string{"sites.md": "Text.\n:::\n"}, "sites.md:2: ::: closes no"},
		{"missing page link", map[string]string{"sites.md": "See [PHP](php.md).\n"}, "sites.md:1: php.md doesn't exist"},
		{"missing image", map[string]string{"sites.md": "![Sites](images/sites.png)\n"}, "images/sites.png doesn't exist"},
		{"not an image", map[string]string{"sites.md": "![Notes](images/notes.txt)\n"}, "images/notes.txt isn't an image"},
		{"outside docs", map[string]string{"sites.md": "See [the README](../README.md).\n"}, "../README.md points outside docs/"},
		{"site path", map[string]string{"sites.md": "See [the roadmap](/changelog#roadmap).\n"}, "/changelog#roadmap: link to another page by its file"},
		{"unknown scheme", map[string]string{"sites.md": "![Sites](shot:sites)\n"}, "shot:sites: link to a file in docs/"},
		{"screenshot as link", map[string]string{"sites.md": "[Sites](screenshot:sites)\n"}, "screenshot:sites is for images"},
		{"bad screenshot id", map[string]string{"sites.md": "![Sites](screenshot:Sites_1)\n"}, "screenshot:Sites_1: an id may only"},
		{"partial screenshot", map[string]string{"sites.md": "![Sites](screenshot:sites)\n", "images/sites-macos-light.png": "png"}, "screenshot:sites is missing images/sites-macos-dark.png, images/sites-windows-light.png, images/sites-windows-dark.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := valid(t)
			write(t, root, tt.files)
			problems, err := lint(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range problems {
				if strings.Contains(p, tt.want) {
					return
				}
			}
			t.Errorf("no problem containing %q in:\n%s", tt.want, strings.Join(problems, "\n"))
		})
	}
}
