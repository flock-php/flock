package issue_triage

import (
	"slices"
	"testing"

	"github.com/google/go-github/v93/github"
)

func TestRunLabelsOutsideContributor(t *testing.T) {
	api := &fakeService{permission: "read"}
	issue := &github.Issue{Number: new(73), User: &github.User{Type: new("User")}}
	if err := Run(api, "flock-php", "flock", issue); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.labels, []string{"needs triage"}) {
		t.Fatalf("labels = %v, want [needs triage]", api.labels)
	}
}

func TestRunLabelsPlatformsForMaintainer(t *testing.T) {
	api := &fakeService{permission: "admin"}
	issue := &github.Issue{
		Number: new(74),
		User:   &github.User{Type: new("User")},
		Body:   new("### Platform\r\n\r\nmacOS, Linux\r\n\r\n### Flock version\r\n\r\n1.0.0"),
	}
	if err := Run(api, "flock-php", "flock", issue); err != nil {
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
	permission string
	labels     []string
}

func (f *fakeService) Permission(string, string, string) (string, error) { return f.permission, nil }
func (f *fakeService) AddLabel(_, _ string, _ int, label string) error {
	f.labels = append(f.labels, label)
	return nil
}
