// Package repository parses GitHub repository identifiers.
package repository

import (
	"fmt"
	"strings"
)

func Parse(value string) (string, string, error) {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY must be owner/repository, got %q", value)
	}
	return owner, name, nil
}
