// Package issue_reference parses issue references from GitHub text.
package issue_reference

import (
	"fmt"
	"regexp"
	"strings"
)

type Reference struct {
	Repository string
	Number     int
}

func (reference Reference) String() string {
	return fmt.Sprintf("%s#%d", reference.Repository, reference.Number)
}

var pattern = regexp.MustCompile(`(?i)(?:^|[^\w/])#(\d+)\b|flock-php/(flock|binaries)(?:#|/issues/)(\d+)\b`)

func Parse(body, repository string) []Reference {
	seen := make(map[Reference]bool)
	var references []Reference
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		reference := Reference{Repository: repository, Number: parseNumber(match[1])}
		if match[1] == "" {
			reference = Reference{Repository: strings.ToLower(match[2]), Number: parseNumber(match[3])}
		}
		if !seen[reference] {
			seen[reference] = true
			references = append(references, reference)
		}
	}
	return references
}

func parseNumber(value string) int {
	number := 0
	for _, digit := range value {
		number = number*10 + int(digit-'0')
	}
	return number
}
