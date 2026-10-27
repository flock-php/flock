package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"flock/internal/api"
	"flock/internal/worktree"
)

var databaseModes = []any{worktree.Shared, worktree.Copy, worktree.Fresh}

func (s *server) addWorktreeTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_worktrees",
		Description: "List the git worktrees Flock serves, each at <branch>.<site>.test: their branch, URL, folder, " +
			"how their setup went and whether they have a database of their own; also the databases Flock kept of " +
			"worktrees that are gone.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, s.listWorktrees)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_worktree",
		Description: "Create a git worktree of a site on a branch (a new branch from `from`, the current commit by default; " +
			"a branch that exists is checked out), next to the site, and wait while Flock sets it up: it copies vendor/, " +
			"node_modules/ and .env from the site, points .env at the worktree's URL, runs the installs and the project's " +
			"hooks. database: shared (the site's, the default), copy (a copy of it) or fresh (empty, migrated). " +
			"When the setup takes longer than about a minute the tool answers that it is still running; list_worktrees shows it.",
		Annotations: additive(false),
		InputSchema: schema[createWorktreeIn](map[string][]any{"database": databaseModes}),
	}, s.createWorktree)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "setup_worktree",
		Description: "Set a worktree up, or wait for the setup Flock runs (Flock sets up worktrees made with git by itself). " +
			"A worktree that is set up already is left alone unless again is true. Removing a worktree or switching its " +
			"database is left to the user (`flock worktree remove`, `flock worktree db`, or the Flock app).",
		Annotations: additive(true),
	}, s.setupWorktree)
}

// worktreeInfo is a worktree in the tools' answers.
type worktreeInfo struct {
	Name   string `json:"name"`
	Site   string `json:"site"`
	Branch string `json:"branch,omitempty"`
	URL    string `json:"url"`
	Path   string `json:"path"`
	// Setup is "queued", "running", "ready" or "failed"; "" when Flock hasn't set it up (it existed
	// before Flock could, or flock.yml turns setting up off).
	Setup string `json:"setup,omitempty"`
	Step  string `json:"step,omitempty"`
	Error string `json:"error,omitempty"`
	// Database is its own database; empty when it uses the site's.
	Database string `json:"database,omitempty"`
	Log      string `json:"log,omitempty"` // for service_logs
	Problem  string `json:"problem,omitempty"`
}

func worktreeInfoOf(st api.SiteStatus) worktreeInfo {
	info := worktreeInfo{Name: st.Name, Site: st.Parent, Branch: st.Branch, URL: siteInfoOf(st).URL, Path: st.Path, Problem: st.Problem}
	if w := st.Worktree; w != nil {
		info.Setup, info.Step, info.Error, info.Database, info.Log = w.State, w.Step, w.Error, w.Database, w.Log
	}
	return info
}

type listWorktreesIn struct {
	Site string `json:"site,omitempty" jsonschema:"only this site's worktrees: a site name, domain or folder; all when empty"`
}

type leftDatabase struct {
	Site     string `json:"site"`
	Database string `json:"database"`
	Path     string `json:"path" jsonschema:"where the worktree was; remove_worktree with it drops the database"`
}

type listWorktreesOut struct {
	Worktrees []worktreeInfo `json:"worktrees"`
	Left      []leftDatabase `json:"left_databases,omitempty"`
}

func (s *server) listWorktrees(ctx context.Context, _ *mcp.CallToolRequest, in listWorktreesIn) (*mcp.CallToolResult, listWorktreesOut, error) {
	status, err := s.Daemon.Status(ctx)
	if err != nil {
		return nil, listWorktreesOut{}, err
	}
	parent := ""
	if in.Site != "" {
		site, err := s.site(ctx, in.Site)
		if err != nil {
			return nil, listWorktreesOut{}, err
		}
		parent = cmpOr(site.Parent, site.Name)
	}
	out := listWorktreesOut{Worktrees: []worktreeInfo{}}
	for _, st := range status.Sites {
		if st.Parent != "" && (parent == "" || st.Parent == parent) {
			out.Worktrees = append(out.Worktrees, worktreeInfoOf(st))
		}
	}
	left, err := s.Daemon.LeftWorktrees(ctx)
	if err != nil {
		return nil, listWorktreesOut{}, err
	}
	for _, e := range left {
		if parent == "" || e.Site == parent {
			out.Left = append(out.Left, leftDatabase{Site: e.Site, Database: e.Database, Path: e.Path})
		}
	}
	return nil, out, nil
}

type worktreeActionOut struct {
	// Done is false when the setup is still running after the tool stopped waiting.
	Done     bool          `json:"done"`
	Message  string        `json:"message"`
	Worktree *worktreeInfo `json:"worktree,omitempty"`
}

type createWorktreeIn struct {
	Branch   string `json:"branch" jsonschema:"the branch: one that exists is checked out, else it is created"`
	Site     string `json:"site,omitempty" jsonschema:"the site: a site name, domain or folder; defaults to the project Flock was started in"`
	From     string `json:"from,omitempty" jsonschema:"where a new branch starts (a branch or commit); defaults to the current commit"`
	Path     string `json:"path,omitempty" jsonschema:"the worktree's folder (absolute); defaults to worktrees.path in flock.yml, else next to the site"`
	Database string `json:"database,omitempty" jsonschema:"shared, copy or fresh; defaults to worktrees.database in flock.yml, else shared"`
}

func (s *server) createWorktree(ctx context.Context, req *mcp.CallToolRequest, in createWorktreeIn) (*mcp.CallToolResult, worktreeActionOut, error) {
	if strings.TrimSpace(in.Branch) == "" {
		return nil, worktreeActionOut{}, errors.New("give the branch")
	}
	site, err := s.site(ctx, in.Site)
	if err != nil {
		return nil, worktreeActionOut{}, err
	}
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return nil, worktreeActionOut{}, errors.New("give the worktree's folder as an absolute path")
	}
	created, err := s.Daemon.CreateWorktree(ctx, api.WorktreeCreate{
		Site: cmpOr(site.Parent, site.Name), Branch: in.Branch, From: in.From, Path: in.Path, DB: in.Database,
	})
	if err != nil {
		return nil, worktreeActionOut{}, err
	}
	return s.awaitWorktree(ctx, req, created.Path, "Created")
}

type setupWorktreeIn struct {
	Worktree string `json:"worktree,omitempty" jsonschema:"the worktree: its name, branch, domain or folder; defaults to the one Flock was started in"`
	Again    bool   `json:"again,omitempty" jsonschema:"set it up once more although it is set up already"`
}

func (s *server) setupWorktree(ctx context.Context, req *mcp.CallToolRequest, in setupWorktreeIn) (*mcp.CallToolResult, worktreeActionOut, error) {
	wt, err := s.worktree(ctx, in.Worktree)
	if err != nil {
		return nil, worktreeActionOut{}, err
	}
	if _, err := s.Daemon.SetupWorktree(ctx, wt.Path, in.Again, ""); err != nil {
		return nil, worktreeActionOut{}, err
	}
	return s.awaitWorktree(ctx, req, wt.Path, "Set up")
}

// awaitWorktree waits for the setup of the worktree at path, as await does for installs.
func (s *server) awaitWorktree(ctx context.Context, req *mcp.CallToolRequest, path, did string) (*mcp.CallToolResult, worktreeActionOut, error) {
	done, text, err := s.await(ctx, req, func(ctx context.Context) (step, error) {
		st, err := s.Daemon.WorktreeSetup(ctx, path)
		if err != nil {
			return step{}, err
		}
		switch st.State {
		case worktree.Ready:
			return step{done: true}, nil
		case worktree.Failed:
			return step{}, fmt.Errorf("setting up %s failed: %s (see service_logs %s)", path, st.Error, st.Log)
		}
		return step{text: cmpOr(st.Step, "waiting for other setups")}, nil
	})
	out := worktreeActionOut{Done: done}
	if status, serr := s.Daemon.Status(ctx); serr == nil {
		for _, st := range status.Sites {
			if st.Parent != "" && filepath.Clean(st.Path) == filepath.Clean(path) {
				info := worktreeInfoOf(st)
				out.Worktree = &info
			}
		}
	}
	if err != nil {
		return nil, out, err
	}
	switch {
	case !done:
		out.Message = fmt.Sprintf("Still setting up %s (%s); list_worktrees shows when it's ready.", path, text)
	case out.Worktree != nil:
		out.Message = fmt.Sprintf("%s: %s is served at %s.", did, out.Worktree.Path, out.Worktree.URL)
		if out.Worktree.Database != "" {
			out.Message += " Its own database: " + out.Worktree.Database + "."
		}
	default:
		out.Message = fmt.Sprintf("%s: %s.", did, path)
	}
	return nil, out, nil
}

// worktree finds the worktree a tool is about: by name, branch, domain or folder, else the one
// the server runs in.
func (s *server) worktree(ctx context.Context, ref string) (api.SiteStatus, error) {
	if ref == "" {
		site, err := s.site(ctx, "")
		if err != nil {
			return api.SiteStatus{}, err
		}
		if site.Parent == "" {
			return api.SiteStatus{}, fmt.Errorf("Flock was started in %s, not in a worktree: pass `worktree` (see list_worktrees)", site.Name)
		}
		return site, nil
	}
	status, err := s.Daemon.Status(ctx)
	if err != nil {
		return api.SiteStatus{}, err
	}
	var worktrees, matches []api.SiteStatus
	for _, st := range status.Sites {
		if st.Parent != "" {
			worktrees = append(worktrees, st)
		}
	}
	if filepath.IsAbs(ref) {
		if wt, ok := siteForDir(worktrees, ref); ok {
			return wt, nil
		}
	}
	for _, wt := range worktrees {
		slug, _, _ := strings.Cut(wt.Name, ".")
		if ref == wt.Name || ref == wt.Domain || ref == wt.Branch || ref == slug {
			matches = append(matches, wt)
		}
	}
	switch len(matches) {
	case 0:
		return api.SiteStatus{}, fmt.Errorf("there is no worktree %q — see list_worktrees", ref)
	case 1:
		return matches[0], nil
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Name
	}
	return api.SiteStatus{}, fmt.Errorf("%q fits several worktrees, name one: %s", ref, strings.Join(names, ", "))
}
