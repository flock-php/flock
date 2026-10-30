package worktree

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Remove runs the before_delete hook in the worktree, then removes it with `git worktree remove`
// (with force, also when it has changes). The branch stays. A failing hook stops the removal
// unless force.
func (j Job) Remove(ctx context.Context, log io.Writer, force bool) error {
	if len(j.Settings.BeforeDelete) > 0 && !j.AsRoot {
		if err := j.RunHooks(ctx, log, "before_delete", j.Settings.BeforeDelete); err != nil {
			if !force {
				return fmt.Errorf("%w — fix it, or remove with --force", err)
			}
			fmt.Fprintf(log, "%v; removing anyway (--force)\n", err)
		}
	}
	args := []string{"-C", j.Site, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.CommandContext(ctx, "git", append(args, j.Path)...)
	cmd.Env = j.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(log, "%s", out)
		return fmt.Errorf("git worktree remove: %s", firstLine(string(out), err))
	}
	return nil
}

func firstLine(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(strings.TrimPrefix(l, "fatal: ")); l != "" {
			return l
		}
	}
	return err.Error()
}
