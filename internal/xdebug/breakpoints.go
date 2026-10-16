// Package xdebug detects which sites want to be debugged. PhpStorm saves a project's
// breakpoints in .idea/workspace.xml; while a site has any, all of its requests go to the PHP
// pool that loads Xdebug, so setting a breakpoint is all it takes to start debugging.
package xdebug

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Workspace is where PhpStorm keeps a project's breakpoints.
func Workspace(project string) string { return filepath.Join(project, ".idea", "workspace.xml") }

// Detector reports whether projects have breakpoints. It re-reads a workspace file only when
// the file changed: PhpStorm rewrites it often (caret positions, open tabs), breakpoints rarely.
type Detector struct {
	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	size  int64
	mtime time.Time
	has   bool
}

// Has reports whether the project at path has an enabled PHP line breakpoint.
func (d *Detector) Has(project string) bool {
	file := Workspace(project)
	info, err := os.Stat(file)
	if err != nil {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.cache[file]; ok && e.size == info.Size() && e.mtime.Equal(info.ModTime()) {
		return e.has
	}

	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	has, err := HasBreakpoints(f)
	if err != nil {
		// PhpStorm may be halfway through writing it; read it again on the next check.
		return false
	}
	if d.cache == nil {
		d.cache = map[string]entry{}
	}
	d.cache[file] = entry{size: info.Size(), mtime: info.ModTime(), has: has}
	return has
}

// HasBreakpoints reports whether a workspace.xml has an enabled PHP line breakpoint:
//
//	<component name="XDebuggerManager">
//	  <breakpoint-manager>
//	    <breakpoints>
//	      <line-breakpoint enabled="true" type="php">
func HasBreakpoints(r io.Reader) (bool, error) {
	dec := xml.NewDecoder(r)
	depth, manager := 0, 0 // manager: depth of the XDebuggerManager component, 0 outside it
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Local == "component" && attr(t, "name") == "XDebuggerManager":
				manager = depth
			case manager > 0 && t.Name.Local == "line-breakpoint" && attr(t, "type") == "php" && attr(t, "enabled") != "false":
				return true, nil
			}
		case xml.EndElement:
			if depth == manager {
				manager = 0
			}
			depth--
		}
	}
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
