package phpbuild

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleasesPicksLatestPatchPerMinorFrom74(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases/active.php":
			_, _ = w.Write([]byte(`{"8":{"8.4":{},"8.5":{}}}`))
		case r.URL.Query().Get("version") == "7":
			_, _ = w.Write([]byte(`{"7.4.33":{},"7.4.9":{},"7.3.33":{}}`))
		case r.URL.Query().Get("version") == "8":
			_, _ = w.Write([]byte(`{"8.5.11":{},"8.5.9":{},"8.4.26":{},"8.4.100":{}}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	got, err := Sources{PHPNet: srv.URL, HTTP: srv.Client()}.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Release{{"8.5", "8.5.11", true}, {"8.4", "8.4.100", true}, {"7.4", "7.4.33", false}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestStaticPHPMinors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<a href="php-8.5.8-fpm-macos-aarch64.tar.gz">x</a> <a href="php-8.4.23-fpm-macos-aarch64.tar.gz">
			<a href="php-8.6.0-cli-macos-aarch64.tar.gz"> <a href="php-8.3.1-fpm-linux-x86_64.tar.gz">`))
	}))
	defer srv.Close()

	got, err := Sources{StaticPHP: srv.URL, HTTP: srv.Client()}.StaticPHPMinors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got["8.5"] || !got["8.4"] || got["8.6"] || got["8.3"] {
		t.Fatalf("got %v (needs fpm builds for this mac only)", got)
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"8.5.11", "8.5.9", 1}, {"8.10", "8.9", 1}, {"7.4", "7.4", 0}, {"", "7.4", -1}, {"8.6.0RC1", "8.6.0", 0}} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if MinorOf("8.5.11") != "8.5" || MinorOf("garbage") != "" {
		t.Fatal("MinorOf")
	}
}
