package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// when it exists, else a new one from from (HEAD when ""). path "" puts it where the site's
// worktrees.path says, relative to the site. It returns the worktree's folder.
func Create(ctx context.Context, site, siteName, slug, branch, from, path string) (string, error) {
	if info, err := os.Stat(filepath.Join(site, ".git")); err != nil || !info.IsDir() {
		return "", invalid("%s is not the main checkout of a git repository", site)
	}
	if slug == "" || strings.HasPrefix(branch, "-") || exec.CommandContext(ctx, "git", "check-ref-format", "--branch", branch).Run() != nil {
		return "", invalid("%q can't name a branch", branch)
	}
	if path == "" {
		settings, err := Load(site, "")
		if err != nil {
			return "", err
		}
		path = Vars{"folder": filepath.Base(site), "branch": slug, "parent": siteName}.Expand(settings.Path)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(site, path)
	}
	path = filepath.Clean(path)
	if exists(path) {
		return "", invalid("%s exists already", path)
	}

	args := []string{"-C", site, "worktree", "add", path, branch}
	if exec.CommandContext(ctx, "git", "-C", site, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() != nil {
		args = []string{"-C", site, "worktree", "add", "-b", branch, path}
		if from != "" {
			args = append(args, from)
		}
	} else if from != "" {
		return "", invalid("the branch %s exists already, so it can't start from %s", branch, from)
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return "", invalid("git worktree add: %s", firstLine(string(out), err))
	}
	return path, nil
}
