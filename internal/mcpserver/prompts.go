package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	currentSiteURI = "flock://site"
	siteURIPrefix  = "flock://sites/"
)

func (s *server) addResources(srv *mcp.Server) {
	srv.AddResource(&mcp.Resource{
		URI:         currentSiteURI,
		Name:        "site_information",
		Title:       "This project's Flock site",
		Description: "The URL, folder, PHP and Node.js versions (and where they come from) and server of the project Flock was started in.",
		MIMEType:    "application/json",
	}, s.readSite)

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: siteURIPrefix + "{name}",
		Name:        "site",
		Title:       "A Flock site",
		Description: "The URL, folder, PHP and Node.js versions (and where they come from) and server of a site.",
		MIMEType:    "application/json",
	}, s.readSite)
}

func (s *server) readSite(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	ref := ""
	if name, ok := strings.CutPrefix(uri, siteURIPrefix); ok {
		ref = name
	} else if uri != currentSiteURI {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	site, err := s.site(ctx, ref)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(siteInfoOf(site), "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}}}, nil
}

func (s *server) addPrompts(srv *mcp.Server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:  "debug_site",
		Title: "Debug a Flock site",
		Description: "Collects what Flock knows about a site (its setup, recent errors, dumps and mail) and asks the agent " +
			"to find out what's wrong.",
		Arguments: []*mcp.PromptArgument{
			{Name: "problem", Description: "what goes wrong, e.g. \"the checkout page shows a 500 error\""},
			{Name: "site", Description: "the site's name or project folder (default: this project)"},
		},
	}, s.debugSite)
}

func (s *server) debugSite(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	site, err := s.site(ctx, req.Params.Arguments["site"])
	if err != nil {
		return nil, err
	}
	info := siteInfoOf(site)

	var b strings.Builder
	fmt.Fprintf(&b, "Find out what's wrong with the Flock site %s (%s, in %s).\n", info.Name, info.URL, info.Path)
	if problem := strings.TrimSpace(req.Params.Arguments["problem"]); problem != "" {
		fmt.Fprintf(&b, "The problem: %s\n", problem)
	}
	b.WriteString("\nThis is what Flock has captured. Use get_errors, get_dumps, list_mail/read_mail and service_logs for more, " +
		"and fix the cause rather than the symptom.\n")

	section := func(title string, v any) {
		data, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintf(&b, "\n## %s\n\n```json\n%s\n```\n", title, data)
	}
	section("Site", info)

	if _, errs, err := s.getErrors(ctx, nil, errorsIn{siteArg: siteArg{Site: site.Name}, Limit: 5}); err == nil {
		section("Recent errors (Laravel log, newest first)", errs)
	}
	if _, dumps, err := s.getDumps(ctx, nil, dumpsIn{Site: site.Name, Limit: 5}); err == nil {
		section("Recent dumps (newest first)", dumps)
	}
	if _, mail, err := s.listMail(ctx, nil, listMailIn{Limit: 5}); err == nil {
		section("Recent mail (newest first)", mail)
	}

	return &mcp.GetPromptResult{
		Description: "Debug " + info.Name,
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: b.String()}}},
	}, nil
}
