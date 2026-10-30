package worktree

import (
	"context"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Branches are a site's git branches, for choosing where a new worktree's branch starts.
type Branches struct {
	// Current is the branch the site's checkout is on ("" when HEAD is detached); Default is the
	// repository's default branch (origin's HEAD, else main or master; "" when it has neither).
	Current string `json:"current"`
	Default string `json:"default"`
	// Local are the branches, Remote the remote-tracking ones (origin/main) as git last fetched
	// them; both sorted by name.
	Local  []string `json:"local"`
	Remote []string `json:"remote"`
}

// ListBranches reads the branches of the repository checked out at site. It doesn't fetch.
func ListBranches(ctx context.Context, site string) (Branches, error) {
	git := func(args ...string) []string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", site}, args...)...).Output()
		if err != nil {
			return nil
		}
		return strings.Fields(string(out))
	}
	if !exists(filepath.Join(site, ".git")) {
		return Branches{}, invalid("%s is not a git repository", site)
	}

	b := Branches{
		Local:  git("for-each-ref", "--format=%(refname:short)", "refs/heads"),
		Remote: []string{},
	}
	if b.Local == nil {
		b.Local = []string{}
	}
	for _, ref := range git("for-each-ref", "--format=%(refname:short)", "refs/remotes") {
		if !strings.HasSuffix(ref, "/HEAD") && strings.Contains(ref, "/") {
			b.Remote = append(b.Remote, ref)
		}
	}
	if current := git("symbolic-ref", "--short", "-q", "HEAD"); len(current) == 1 {
		b.Current = current[0]
	}
	if head := git("symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); len(head) == 1 {
		b.Default = strings.TrimPrefix(head[0], "origin/")
	} else if i := slices.IndexFunc(b.Local, func(n string) bool { return n == "main" || n == "master" }); i >= 0 {
		b.Default = b.Local[i]
	}
	slices.Sort(b.Local)
	slices.Sort(b.Remote)
	return b, nil
}

var (
	adjectives = []string{
		"brave", "bright", "calm", "clever", "cosy", "daring", "eager", "fancy", "gentle", "glad", "happy",
		"jolly", "keen", "kind", "lively", "lucky", "merry", "mighty", "nimble", "proud", "quick", "quiet",
		"rapid", "shiny", "silent", "sleepy", "snappy", "sunny", "swift", "tidy", "vivid", "witty",
	}
	animals = []string{
		"badger", "beaver", "bison", "crane", "falcon", "ferret", "finch", "fox", "gecko", "heron", "ibex",
		"jaguar", "koala", "lemur", "lynx", "marten", "moose", "newt", "otter", "owl", "panda", "puffin",
		"raven", "robin", "seal", "sparrow", "stork", "swan", "tapir", "tiger", "walrus", "wren",
	}
)

// NewBranchName makes up a name for a worktree created without one, like brave-otter: a branch
// name and a subdomain at once. taken reports names already in use; when many are, a number is
// added.
func NewBranchName(taken func(string) bool) string {
	name := ""
	for range 100 {
		name = adjectives[rand.IntN(len(adjectives))] + "-" + animals[rand.IntN(len(animals))]
		if !taken(name) {
			return name
		}
	}
	for n := 2; ; n++ {
		if numbered := name + "-" + strconv.Itoa(n); !taken(numbered) {
			return numbered
		}
	}
}
