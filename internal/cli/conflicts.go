package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/config"
	"flock/internal/dns"
	"flock/internal/ports"
)

// conflict is another program that keeps Flock from working while it runs: it listens on one of
// Flock's ports, or answers for the TLD (macOS' resolver file).
type conflict struct {
	Port    int    `json:"port,omitempty"`
	File    string `json:"file,omitempty"` // the resolver file
	Service string `json:"service"`        // what Flock needs it for: "HTTP", "*.test DNS"
	// Holder names the program ("Herd (nginx-arm64, pid 812)"), App what to quit ("Herd", or
	// the holder when no app is known); the apps group the conflicts by it.
	Holder  string `json:"holder"`
	App     string `json:"app"`
	Hint    string `json:"hint,omitempty"` // "quit Herd"
	Message string `json:"message"`
}

func newConflictsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "List other programs (e.g. another local PHP environment) that use the ports Flock needs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			found := findConflicts(s)
			if asJSON {
				if found == nil {
					found = []conflict{}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"conflicts": found})
			}
			if len(found) == 0 {
				cmd.Println("✓ no other program uses Flock's ports")
				return nil
			}
			return conflictsError(found)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the conflicts as JSON (an empty list when there are none)")
	return cmd
}

// errConflicts is what the apps look for to tell conflicts from other failures of `flock install`.
var errConflicts = errors.New("other programs use the ports Flock needs")

// conflictsError lists the conflicts and what to quit.
func conflictsError(found []conflict) error {
	var b strings.Builder
	var apps []string
	for _, c := range found {
		b.WriteString("\n  • " + c.Message)
		if !slices.Contains(apps, c.App) {
			apps = append(apps, c.App)
		}
	}
	return fmt.Errorf("%w:%s\nQuit %s first, then try again.", errConflicts, b.String(), joinAnd(apps))
}

// joinAnd joins "a", "b" and "c" as "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// corePort is a port the daemon itself listens on, needed from the start.
type corePort struct {
	port    int
	service string
}

// corePorts are the ports Flock needs before any service is installed. The optional services
// (PostgreSQL, MySQL, ...) are checked when they are installed. On Windows, DNS is left out: it
// has to be port 53 there, and the hosts file works without it.
func corePorts(cfg *config.Config) []corePort {
	core := []corePort{
		{cfg.Ports.HTTP, "HTTP"},
		{cfg.Ports.HTTPS, "HTTPS"},
		{cfg.Ports.SMTP, "mail"},
		{cfg.Ports.API, "app API"},
	}
	if runtime.GOOS != "windows" {
		core = append(core, corePort{cfg.Ports.DNS, "*." + cfg.TLD + " DNS"})
	}
	if cfg.Dumps {
		core = append(core, corePort{cfg.Ports.Dumps, "dumps"}, corePort{cfg.Ports.Agent, "events"})
	}
	return core
}

// findConflicts checks Flock's core ports and, on macOS, whether another program answers for the TLD.
func findConflicts(s *state) []conflict {
	var found []conflict
	seen := map[int]bool{}
	for _, p := range corePorts(s.cfg) {
		if seen[p.port] {
			continue
		}
		seen[p.port] = true
		for _, h := range ports.Foreign(p.port, s.root) {
			found = append(found, portConflict(p, h))
		}
	}
	if c, ok := resolverConflict(s); ok {
		found = append(found, c)
	}
	return found
}

func portConflict(p corePort, h ports.Holder) conflict {
	c := conflict{Port: p.port, Service: p.service, Holder: h.String(), App: holderApp(h), Hint: h.Hint()}
	c.Message = fmt.Sprintf("port %d (%s) is used by %s", p.port, p.service, c.Holder)
	if c.Hint != "" {
		c.Message += " — " + c.Hint
	}
	return c
}

// holderApp is what the user has to quit: the app, else the program.
func holderApp(h ports.Holder) string {
	switch {
	case h.Brew != "":
		return "Homebrew's " + h.Brew
	case h.App != "":
		return h.App
	case h.Process != "":
		return h.Process
	}
	return "the other program"
}

// resolverConflict reports another program's resolver file for the TLD (macOS sends *.test
// lookups wherever it says) while that program's DNS server runs. A leftover file of a program
// that no longer runs is no conflict: `flock install` replaces it.
func resolverConflict(s *state) (conflict, bool) {
	if runtime.GOOS != "darwin" {
		return conflict{}, false
	}
	file := dns.ResolverFile(s.cfg.TLD)
	data, err := os.ReadFile(file)
	if err != nil || strings.HasPrefix(string(data), "# Managed by Flock") {
		return conflict{}, false
	}
	port := resolverPort(string(data))
	if port == 0 {
		return conflict{}, false
	}
	holders := ports.Foreign(port, s.root)
	if len(holders) == 0 {
		return conflict{}, false
	}
	h := holders[0]
	c := conflict{File: file, Service: "*." + s.cfg.TLD + " DNS", Holder: h.String(), App: holderApp(h), Hint: h.Hint()}
	c.Message = fmt.Sprintf("%s sends *.%s to %s (port %d)", file, s.cfg.TLD, c.Holder, port)
	if c.Hint != "" {
		c.Message += " — " + c.Hint
	}
	return c, true
}

// resolverPort is the local port a resolver file sends lookups to, or 0 when its nameserver
// isn't on this machine.
func resolverPort(content string) int {
	local, port := false, 53
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			local = local || fields[1] == "127.0.0.1" || fields[1] == "::1"
		case "port":
			if p, err := strconv.Atoi(fields[1]); err == nil {
				port = p
			}
		}
	}
	if !local {
		return 0
	}
	return port
}
