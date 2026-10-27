package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Worktree is a git worktree of a site's repository, as git records it in <site>/.git/worktrees.
type Worktree struct {
	Path   string // the worktree's folder
	Branch string // the checked-out branch, "" when HEAD is detached
	ID     string // git's name for it: the folder under .git/worktrees, usually the worktree's base name
}

// Worktrees lists the worktrees of the repository checked out at repo. Only a main checkout has
// them: in a worktree (or a submodule), .git is a file. A worktree git is still writing (its HEAD
// is the null id `git worktree add` starts with) or whose folder is gone is left out.
func Worktrees(repo string) []Worktree {
	dir := filepath.Join(repo, ".git", "worktrees")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []Worktree
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		admin := filepath.Join(dir, e.Name())
		gitdir, err := os.ReadFile(filepath.Join(admin, "gitdir"))
		if err != nil {
			continue
		}
		// gitdir points at the worktree's .git file; relative with worktree.useRelativePaths.
		dotGit := strings.TrimSpace(string(gitdir))
		if !filepath.IsAbs(dotGit) {
			dotGit = filepath.Join(admin, dotGit)
		}
		path := filepath.Dir(filepath.Clean(dotGit))
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}

		head, err := os.ReadFile(filepath.Join(admin, "HEAD"))
		if err != nil {
			continue
		}
		ref := strings.TrimSpace(string(head))
		branch, symbolic := strings.CutPrefix(ref, "ref: refs/heads/")
		if !symbolic {
			if strings.Trim(ref, "0") == "" {
				continue // still being created
			}
			branch = ""
		}
		out = append(out, Worktree{Path: path, Branch: branch, ID: e.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// WorktreeSlug turns a branch name into the label a worktree is served under: feature/Auth_v2.1
// becomes feature-auth-v2-1.
func WorktreeSlug(branch string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(branch) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '/', r == '_', r == '.':
			b.WriteRune('-')
		}
	}
	slug := b.String()
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 63 {
		slug = strings.TrimRight(slug[:63], "-")
	}
	return slug
}

// IsWorktree reports whether the site is a git worktree served under its parent site.
func (s Site) IsWorktree() bool { return s.Parent != "" }

// worktreeSites finds the worktrees of the sites in byName and adds them to it as <slug>.<parent>,
// so they are served at <slug>.<parent domain>. A folder linked as a site of its own stays that
// site; a parked folder that is a worktree is served as the worktree instead, not twice.
func worktreeSites(byName map[string]Site) {
	parents := make([]Site, 0, len(byName))
	for _, s := range byName {
		parents = append(parents, s)
	}
	sort.Slice(parents, func(i, j int) bool { return parents[i].Name < parents[j].Name })

	var byPath map[string]string // real path → site name, built once a site has worktrees
	for _, parent := range parents {
		for _, wt := range Worktrees(parent.Path) {
			if byPath == nil {
				byPath = map[string]string{}
				for _, s := range parents {
					byPath[realPath(s.Path)] = s.Name
				}
			}
			if owner, ok := byPath[realPath(wt.Path)]; ok {
				if !byName[owner].Parked {
					continue
				}
				delete(byName, owner)
			}

			slug := WorktreeSlug(wt.Branch)
			if slug == "" {
				slug = WorktreeSlug(wt.ID)
			}
			if slug == "" {
				continue
			}
			name := slug + "." + parent.Name
			for n := 2; ; n++ {
				if _, taken := byName[name]; !taken {
					break
				}
				name = fmt.Sprintf("%s-%d.%s", slug, n, parent.Name)
			}

			// Settings follow the parent; the server too, except FrankenPHP, whose process and
			// port are the parent's own.
			byName[name] = Site{
				Name: name, Path: wt.Path, PHP: parent.PHP, Node: parent.Node, Secured: parent.Secured,
				Root: parent.Root, Parent: parent.Name, Branch: wt.Branch,
			}
		}
	}
}

// realPath resolves symlinks so a parked folder and the path git recorded compare equal
// (/tmp vs /private/tmp on macOS); it falls back to the cleaned path.
func realPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
