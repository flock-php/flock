package issue_triage

import (
	"testing"

	"github.com/google/go-github/v93/github"
)

func TestRunLabelsOutsideContributor(t *testing.T) {
	api := &fakeService{}
	issue := &github.Issue{Number: new(73), User: &github.User{Type: new("User")}}
	if err := Run(api, "flock-php", "flock", issue); err != nil {
		t.Fatal(err)
	}
	if !api.labeled {
		t.Fatal("issue was not labeled")
	}
}

type fakeService struct{ labeled bool }

func (f *fakeService) Permission(string, string, string) (string, error) { return "read", nil }
func (f *fakeService) AddLabel(string, string, int, string) error {
	f.labeled = true
	return nil
}
