// Package issue_reference defines issue references used by contribution workflows.
package issue_reference

import "fmt"

type Reference struct {
	Repository string
	Number     int
}

func (reference Reference) String() string {
	return fmt.Sprintf("%s#%d", reference.Repository, reference.Number)
}
