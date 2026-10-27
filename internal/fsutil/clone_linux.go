package fsutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// cloneDir has no single call on Linux: CloneTree clones file by file.
func cloneDir(_, _ string) error { return errors.ErrUnsupported }

// cloneFile shares src's blocks with a new dst through the FICLONE ioctl (btrfs, XFS with
// reflink, bcachefs); other filesystems refuse it and dst is removed again.
func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	err = unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}
