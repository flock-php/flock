package worktree

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeServer is a database server that keeps its databases' names.
type fakeServer struct {
	dbs  []string
	fail error // what Create and Copy return
}

func (s *fakeServer) Create(_ context.Context, name string) error {
	if s.fail != nil {
		return s.fail
	}
	if slices.Contains(s.dbs, name) {
		return ErrExists
	}
	s.dbs = append(s.dbs, name)
	return nil
}

func (s *fakeServer) Copy(ctx context.Context, from, to string, _ io.Writer) error {
	if !slices.Contains(s.dbs, from) {
		return errors.New("no database " + from)
	}
	return s.Create(ctx, to)
}

func (s *fakeServer) Drop(_ context.Context, name string) error {
	s.dbs = slices.DeleteFunc(s.dbs, func(d string) bool { return d == name })
	return nil
}

func TestDatabaseName(t *testing.T) {
	if got := DatabaseName("Acme-Shop", "feature-auth"); got != "acme_shop_feature_auth" {
		t.Errorf("got %q", got)
	}
	long := DatabaseName("acme", strings.Repeat("x", 80))
	if len(long) != 63 || long == DatabaseName("acme", strings.Repeat("x", 79)) {
		t.Errorf("long names are cut to 63 and stay apart: %q", long)
	}
}

// dbJob is a worktree of a site whose .env is env, with the fake server on 5432 and 3306.
func dbJob(t *testing.T, env string, server *fakeServer) *Job {
	t.Helper()
	site, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(site, ".env"), env)
	write(t, filepath.Join(wt, ".env"), env)
	return &Job{Site: site, Path: wt, Domain: "feature-auth.acme.test", ParentDomain: "acme.test",
		Settings: Defaults(), Servers: func(engine string, port int) Server {
			if (engine == "pgsql" && port == 5432) || (engine == "mysql" && port == 3306) {
				return server
			}
			return nil
		}}
}

func TestServerDatabases(t *testing.T) {
	ctx, log := context.Background(), &bytes.Buffer{}

	server := &fakeServer{dbs: []string{"acme"}}
	j := dbJob(t, "DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_DATABASE=acme\n", server)
	j.DBMode = Copy
	var recorded Database
	j.OnDatabase = func(db Database) { recorded = db }
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	want := Database{Engine: "pgsql", Name: "acme_feature_auth"}
	if recorded != want || !slices.Equal(server.dbs, []string{"acme", "acme_feature_auth"}) {
		t.Fatalf("copy: recorded %+v, server %v", recorded, server.dbs)
	}
	if !strings.Contains(read(t, filepath.Join(j.Path, ".env")), "DB_DATABASE=acme_feature_auth\n") {
		t.Errorf(".env: %s", read(t, filepath.Join(j.Path, ".env")))
	}

	// Set up again, it keeps the database it made.
	j.Database = recorded
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil || len(server.dbs) != 2 {
		t.Fatalf("again: %v %v", err, server.dbs)
	}

	// Back to the site's: dropped, .env names the site's again.
	j.DBMode = Shared
	if err := j.SwitchDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(server.dbs, []string{"acme"}) || recorded != (Database{}) {
		t.Errorf("shared: recorded %+v, server %v", recorded, server.dbs)
	}
	if !strings.Contains(read(t, filepath.Join(j.Path, ".env")), "DB_DATABASE=acme\n") {
		t.Errorf(".env: %s", read(t, filepath.Join(j.Path, ".env")))
	}

	// A database Flock didn't make is never used: the worktree stays on the site's.
	server.dbs = append(server.dbs, "acme_feature_auth")
	j.DBMode = Fresh
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if recorded != (Database{}) || !strings.Contains(log.String(), "Flock didn't make it") {
		t.Errorf("an existing database: recorded %+v\n%s", recorded, log)
	}

	// A server Flock doesn't run, or one that fails: the site's database, and the log says why.
	for _, env := range []string{"DB_CONNECTION=pgsql\nDB_HOST=db.example.com\nDB_DATABASE=acme\n", "DB_CONNECTION=mysql\nDB_DATABASE=acme\n"} {
		failing := &fakeServer{fail: errors.New("access denied")}
		j := dbJob(t, env, failing)
		j.DBMode = Fresh
		log.Reset()
		if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil || j.Database != (Database{}) {
			t.Fatalf("%q: %v %+v", env, err, j.Database)
		}
		if !strings.Contains(log.String(), "uses the site's database") {
			t.Errorf("%q: %s", env, log)
		}
	}
}

func TestSQLiteDatabases(t *testing.T) {
	ctx, log := context.Background(), &bytes.Buffer{}
	j := dbJob(t, "DB_CONNECTION=sqlite\n", nil)
	write(t, filepath.Join(j.Site, "database", "database.sqlite"), "site data")

	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(j.Path, ".env")), "DB_DATABASE="+filepath.Join(j.Site, "database", "database.sqlite")) {
		t.Errorf("shared: %s", read(t, filepath.Join(j.Path, ".env")))
	}

	j.DBMode = Copy
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(j.Path, "database", "database.sqlite")
	if j.Database != (Database{Engine: "sqlite", Name: file}) || read(t, file) != "site data" {
		t.Fatalf("copy: %+v", j.Database)
	}

	j.DBMode = Fresh
	if err := j.SwitchDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if read(t, file) != "" || read(t, filepath.Join(j.Site, "database", "database.sqlite")) != "site data" {
		t.Error("fresh: the copy wasn't replaced by an empty file, or the site's changed")
	}
	if err := j.DropDatabase(ctx, log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("dropped, the file is still there: %v", err)
	}
}
