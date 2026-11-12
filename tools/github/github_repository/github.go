// Package github_repository implements GitHub repository operations used by workflows.
package github_repository

import (
	"context"

	"github.com/google/go-github/v93/github"
)

type Service struct {
	client *github.Client
}

func New(client *github.Client) *Service {
	return &Service{client: client}
}

func (s *Service) GetUserPermission(owner, repository, username string) (string, error) {
	permission, err := s.getUserPermission(owner, repository, username)
	if err != nil {
		return "", err
	}
	return permission.GetPermission(), nil
}

func (s *Service) GetUserRole(owner, repository, username string) (string, error) {
	permission, err := s.getUserPermission(owner, repository, username)
	if err != nil {
		return "", err
	}
	return permission.GetRoleName(), nil
}

func (s *Service) getUserPermission(owner, repository, username string) (*github.RepositoryPermissionLevel, error) {
	permission, _, err := s.client.Repositories.GetPermissionLevel(context.Background(), owner, repository, username)
	return permission, err
}
