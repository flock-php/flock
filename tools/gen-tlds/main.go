// Command gen-tlds regenerates internal/config/tlds.txt: the top-level domain labels of the
// public DNS root zone, which config.ValidateTLD refuses as a local TLD. It fetches the list
// IANA publishes (https://data.iana.org/TLD/tlds-alpha-by-domain.txt — one UPPERCASE label per
// line, a '#' comment on top), keeps the labels lowercase and sorted, one per line. Run it from
// the repository root when the list should be refreshed:
//
//	go run ./tools/gen-tlds
//
// -source reads a saved copy of the file instead of fetching it, so refreshing needs no network.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	url    = "https://data.iana.org/TLD/tlds-alpha-by-domain.txt"
	output = "internal/config/tlds.txt"
)

func main() {
	source := flag.String("source", "", "read the IANA list from this file instead of fetching it")
	flag.Parse()

	if err := run(*source); err != nil {
		log.Fatal(err)
	}
}

func run(source string) error {
	var r io.Reader
	if source != "" {
		f, err := os.Open(source)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	} else {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fetch %s: %s", url, resp.Status)
		}
		r = resp.Body
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	labels, err := parse(data)
	if err != nil {
		return err
	}

	if err := os.WriteFile(output, []byte(strings.Join(labels, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d labels\n", output, len(labels))
	return nil
}

// parse keeps the labels of the list: lowercased, sorted, comments and blank lines dropped.
func parse(data []byte) ([]string, error) {
	var labels []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		labels = append(labels, strings.ToLower(line))
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("no TLD labels in the list")
	}
	slices.Sort(labels)
	return labels, nil
}
