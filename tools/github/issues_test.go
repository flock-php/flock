package main

import (
	"errors"
	"slices"
	"testing"

	"flock/tools/github/github_issue"
	"flock/tools/github/issue_triage"
)

func TestIssuePolicies(t *testing.T) {
	tests := []struct {
		name             string
		events           []string
		triaged, removed bool
		permission       string
	}{
		{"external unassigned", []string{`{"action":"opened","issue":{"number":73,"user":{"login":"contributor","type":"User"},"labels":[{"name":"help wanted"}]}}`}, true, false, ""},
		{"external assigned", []string{`{"action":"opened","issue":{"number":73,"user":{"login":"contributor","type":"User"},"labels":[{"name":"help wanted"}]}}`, `{"action":"assigned","issue":{"number":73,"assignees":[{"login":"contributor"}],"labels":[{"name":"help wanted"}]}}`}, true, true, ""},
		{"member assigned", []string{`{"action":"opened","issue":{"number":73,"user":{"login":"maintainer","type":"User"},"author_association":"MEMBER","labels":[{"name":"help wanted"}]}}`, `{"action":"assigned","issue":{"number":73,"assignees":[{"login":"contributor"}],"labels":[{"name":"help wanted"}]}}`}, false, true, ""},
		{"bot", []string{`{"action":"opened","issue":{"number":73,"user":{"login":"bot","type":"Bot"}}}`}, false, false, ""},
		{"writer", []string{`{"action":"opened","issue":{"number":73,"user":{"login":"writer","type":"User"}}}`}, false, false, "write"},
		{"assigned without assignees", []string{`{"action":"assigned","issue":{"number":73,"labels":[{"name":"help wanted"}]}}`}, false, false, ""},
		{"unsupported event", []string{`{"action":"labeled","issue":{"number":73,"user":{"login":"contributor","type":"User"}}}`}, false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &issueEventFakeService{permission: tt.permission}
			for _, data := range tt.events {
				event, err := github_issue.ParseEvent([]byte(data))
				if err != nil {
					t.Fatal(err)
				}
				api.currentIssue = &event.Issue
				if err := newIssueEventService(api).Run("flock-php", "flock", []byte(data)); err != nil {
					t.Fatal(err)
				}
			}
			if api.triaged != tt.triaged || api.removed != tt.removed {
				t.Fatalf("triaged = %t, removed = %t", api.triaged, api.removed)
			}
		})
	}
}

func TestIssuePoliciesLabelsPlatforms(t *testing.T) {
	api := &issueEventFakeService{permission: "admin"}
	data := []byte(`{"action":"opened","issue":{"number":73,"user":{"login":"maintainer","type":"User"},"body":"### Platform\n\nmacOS, Linux\n\n### Flock version\n\n1.0.0"}}`)
	if err := newIssueEventService(api).Run("flock-php", "flock", data); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.labels, []string{"os: mac", "os: linux"}) {
		t.Fatalf("labels = %v, want [os: mac os: linux]", api.labels)
	}
}

func TestIssueEventStopsOnParseAndServiceErrors(t *testing.T) {
	tests := []struct {
		name string
		api  issueEventFakeService
		data string
	}{
		{name: "invalid payload", data: `{`},
		{name: "permission", api: issueEventFakeService{permissionErr: errors.New("permission")}, data: `{"action":"opened","issue":{"number":73,"user":{"login":"contributor","type":"User"}}}`},
		{name: "label", api: issueEventFakeService{labelErr: errors.New("label")}, data: `{"action":"opened","issue":{"number":73,"user":{"login":"contributor","type":"User"}}}`},
		{name: "remove label", api: issueEventFakeService{currentIssue: &github_issue.Issue{Number: 73, Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: "help wanted"}}}, removeErr: errors.New("remove")}, data: `{"action":"assigned","issue":{"number":73,"assignees":[{"login":"contributor"}],"labels":[{"name":"help wanted"}]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := newIssueEventService(&tt.api).Run("flock-php", "flock", []byte(tt.data)); err == nil {
				t.Fatal("Run() returned nil error")
			}
		})
	}
}

type issueEventFakeService struct {
	triaged, removed                   bool
	currentIssue                       *github_issue.Issue
	permission                         string
	permissionErr, labelErr, removeErr error
	labels                             []string
}

func newIssueEventService(issues *issueEventFakeService) IssueEventService {
	return IssueEventService{Assignment: issues, Triage: issue_triage.Dependencies{Issues: issues, Repositories: issues}}
}
func (f *issueEventFakeService) Get(string, string, int) (*github_issue.Issue, error) {
	return f.currentIssue, nil
}
func (f *issueEventFakeService) GetUserPermission(string, string, string) (string, error) {
	if f.permission == "" {
		return "read", f.permissionErr
	}
	return f.permission, f.permissionErr
}
func (f *issueEventFakeService) AddLabel(_ string, _ string, _ int, label string) error {
	f.labels = append(f.labels, label)
	f.triaged = label == "needs triage"
	return f.labelErr
}
func (f *issueEventFakeService) RemoveHelpWanted(string, string, *github_issue.Issue) error {
	f.removed = true
	return f.removeErr
}
