package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"flock/internal/config"
)

// ErrInvalid marks a worktree that can't be created as asked: a bad branch name, a site that
// isn't a main checkout, a folder that exists, git refusing.
var ErrInvalid = errors.New("invalid worktree")

// invalidError is ErrInvalid with a message of its own.
type invalidError string

func (e invalidError) Error() string        { return string(e) }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(format string, args ...any) error { return invalidError(fmt.Sprintf(format, args...)) }

// Create makes a git worktree of the site at site (served as siteName) on branch: the branch
// when it exists, else a new one from from (HEAD when ""); without a branch, one with a name of
// its own (see NewBranchName). path "" puts it where the site's worktrees.path says, relative to
// the site. It returns the worktree's folder and its branch.
func Create(ctx context.Context, site, siteName, branch, from, path string) (string, string, error) {
	if info, err := os.Stat(filepath.Join(site, ".git")); err != nil || !info.IsDir() {
		return "", "", invalid("%s is not the main checkout of a git repository", site)
	}
	branchExists := func(name string) bool {
		return exec.CommandContext(ctx, "git", "-C", site, "rev-parse", "--verify", "--quiet", "refs/heads/"+name).Run() == nil
	}
	var settings Settings
	if path == "" {
		var err error
		if settings, err = Load(site, ""); err != nil {
			return "", "", err
		}
	}
	// Where the worktree of a branch goes, when path doesn't say.
	at := func(branch string) string {
		if path != "" {
			return path
		}
		return Vars{"folder": filepath.Base(site), "branch": config.WorktreeSlug(branch), "parent": siteName}.Expand(settings.Path)
	}
	if branch == "" {
		branch = NewBranchName(func(name string) bool { return branchExists(name) || exists(abs(site, at(name))) })
	}
	slug := config.WorktreeSlug(branch)
	if slug == "" || strings.HasPrefix(branch, "-") || exec.CommandContext(ctx, "git", "check-ref-format", "--branch", branch).Run() != nil {
		return "", "", invalid("%q can't name a branch", branch)
	}
	path = at(branch)
	path = abs(site, path)
	if exists(path) {
		return "", "", invalid("%s exists already", path)
	}

	args := []string{"-C", site, "worktree", "add", path, branch}
	if !branchExists(branch) {
		args = []string{"-C", site, "worktree", "add", "-b", branch, path}
		if from != "" {
			args = append(args, from)
		}
	} else if from != "" {
		return "", "", invalid("the branch %s exists already, so it can't start from %s", branch, from)
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return "", "", invalid("git worktree add: %s", firstLine(string(out), err))
	}
	return path, branch, nil
}

// abs makes path absolute, relative to dir.
func abs(dir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}
