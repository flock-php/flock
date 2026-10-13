//go:build !windows

package node

import "os"

func symlinkDir(target, link string) error { return os.Symlink(target, link) }
