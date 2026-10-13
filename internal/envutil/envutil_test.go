package envutil

import (
	"slices"
	"testing"
)

func TestMergeReplacesInheritedKeys(t *testing.T) {
	got := Merge(
		[]string{"PATH=/usr/bin", "PHP_INI_SCAN_DIR=/other/php", "HOME=/u"},
		[]string{"PHP_INI_SCAN_DIR=/flock/conf.d", "PHPRC=/flock/php.ini"},
	)
	want := []string{"PATH=/usr/bin", "HOME=/u", "PHP_INI_SCAN_DIR=/flock/conf.d", "PHPRC=/flock/php.ini"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}
