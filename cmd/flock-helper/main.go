// flock-helper is the Windows hosts helper service (see package hosts), installed by `flock install`.
//
// It is a separate program rather than a mode of flock.exe because Caddy, which flock.exe links,
// registers its own Windows service handler in an init function whenever the process runs as a
// service, leaving no way to run another one. It also keeps the program that runs as LocalSystem small.
package main

import (
	"flag"
	"fmt"
	"os"

	"flock/internal/hosts"
)

// version is set by the build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	source := flag.String("source", "", "file the daemon lists its domains in")
	tld := flag.String("tld", "", "only names under this TLD are accepted")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if err := hosts.RunHelper(*source, *tld); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
