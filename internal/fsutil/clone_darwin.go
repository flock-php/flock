package fsutil

import "golang.org/x/sys/unix"

// cloneDir clones a whole folder in one call: clonefile(2) copies directories recursively on APFS.
func cloneDir(src, dst string) error { return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) }

func cloneFile(src, dst string) error { return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) }
