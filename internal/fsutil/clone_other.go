//go:build !darwin && !linux

package fsutil

import "errors"

// Windows can clone blocks on ReFS and Dev Drive (FSCTL_DUPLICATE_EXTENTS_TO_FILE), which Flock
// doesn't use yet: files are copied.
func cloneDir(_, _ string) error { return errors.ErrUnsupported }

func cloneFile(_, _ string) error { return errors.ErrUnsupported }
