package postgres

import "path/filepath"

// ClientTools are the client programs Flock exposes as shims. They come with every installed major.
var ClientTools = []string{"psql", "pg_dump", "pg_dumpall", "pg_restore", "createdb", "dropdb"}

// ClientTool is a client tool ("psql") of the newest installed major: pg_dump has to be at least
// as new as the server it dumps, so that one works with every installed server. ok is false when
// no PostgreSQL is installed.
func ClientTool(root, tool string) (path string, ok bool) {
	ids, _ := InstalledIDs(root)
	if len(ids) == 0 {
		return "", false
	}
	return filepath.Join(BinDir(root, ids[0]), exe(tool)), true
}
