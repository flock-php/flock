package issue_triage

import (
	"errors"
	"slices"
	"testing"

	"flock/tools/github/github_issue"
)

func TestRunLabelsOutsideContributor(t *testing.T) {
	api := &fakeService{permission: "read"}
	issue := &github_issue.Issue{Number: 73, User: github_issue.User{Type: "User"}}
	if err := Execute(Dependencies{Issues: api, Repositories: api}, "flock-php", "flock", issue); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.labels, []string{"needs triage"}) {
		t.Fatalf("labels = %v, want [needs triage]", api.labels)
	}
}

func TestExecuteReturnsPermissionAndLabelErrors(t *testing.T) {
	issue := &github_issue.Issue{Number: 73, User: github_issue.User{Type: "User"}}
	for _, api := range []*fakeService{{permissionErr: errors.New("permission")}, {labelErr: errors.New("label")}} {
		if err := Execute(Dependencies{Issues: api, Repositories: api}, "flock-php", "flock", issue); err == nil {
			t.Fatal("Execute() returned nil error")
		}
	}
}

func TestRunLabelsPlatformsForMaintainer(t *testing.T) {
	api := &fakeService{permission: "admin"}
	issue := &github_issue.Issue{
		Number: 74,
		User:   github_issue.User{Type: "User"},
		Body:   "### Platform\r\n\r\nmacOS, Linux\r\n\r\n### Flock version\r\n\r\n1.0.0",
	}
	if err := Execute(Dependencies{Issues: api, Repositories: api}, "flock-php", "flock", issue); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.labels, []string{"os: mac", "os: linux"}) {
		t.Fatalf("labels = %v, want [os: mac os: linux]", api.labels)
	}
}

func TestPlatforms(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"one", "### Platform\n\nWindows\n", []string{"os: windows"}},
		{"all", "### Platform\n\nmacOS, Windows, Linux", []string{"os: mac", "os: windows", "os: linux"}},
		{"no response", "### Platform\n\n_No response_\n\n### Logs", nil},
		{"no field", "Something broke on macOS", nil},
		{"empty", "", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := platforms(test.body); !slices.Equal(got, test.want) {
				t.Fatalf("platforms() = %v, want %v", got, test.want)
			}
		})
	}
}

type fakeService struct {
	permission    string
	labels        []string
	permissionErr error
	labelErr      error
}

func (f *fakeService) GetUserPermission(string, string, string) (string, error) {
	if f.permission == "" {
		return "read", f.permissionErr
	}
	return f.permission, f.permissionErr
}
func (f *fakeService) AddLabel(_, _ string, _ int, label string) error {
	f.labels = append(f.labels, label)
	return f.labelErr
}
