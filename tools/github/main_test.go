package main

import "testing"

func TestParseRepository(t *testing.T) {
	owner, repository, err := parseRepository("flock-php/flock")
	if err != nil || owner != "flock-php" || repository != "flock" {
		t.Fatalf("parseRepository() = %q, %q, %v", owner, repository, err)
	}
	if _, _, err := parseRepository("flock-php"); err == nil {
		t.Fatal("malformed repository returned nil error")
	}
}
