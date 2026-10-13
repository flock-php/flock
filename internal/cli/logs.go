package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newLogsCommand() *cobra.Command {
	var lines int
	var follow bool

	cmd := &cobra.Command{
		Use:   "logs [name]",
		Short: "Show Flock's service logs (daemon, web, php8.5-fpm, postgres, ...)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			dir := filepath.Join(s.root, "log")

			names, err := logNames(dir)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				cmd.Println("available logs (flock logs <name> [-f]):")
				for _, n := range names {
					cmd.Println("  " + n)
				}
				return nil
			}

			name := strings.TrimSuffix(args[0], ".log")
			path := filepath.Join(dir, name+".log")
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("no log named %q (available: %s)", name, strings.Join(names, ", "))
			}

			return tailFile(cmd, path, lines, follow)
		},
	}

	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "number of lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")

	return cmd
}

func logNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			names = append(names, strings.TrimSuffix(e.Name(), ".log"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// tailFile prints the last n lines of path and, with follow, everything appended afterwards.
func tailFile(cmd *cobra.Command, path string, n int, follow bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	out := cmd.OutOrStdout()
	offset, err := lastLinesOffset(f, n)
	if err != nil {
		return err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.Copy(out, f); err != nil {
		return err
	}
	if !follow {
		return nil
	}

	reader := bufio.NewReader(f)
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		default:
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = out.Write(line)
		}
		if err == io.EOF {
			// Log rotated or truncated: start over from the beginning.
			if pos, _ := f.Seek(0, io.SeekCurrent); fileSize(path) < pos {
				_, _ = f.Seek(0, io.SeekStart)
				reader.Reset(f)
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}

// lastLinesOffset finds where the last n lines of f start by reading backwards in chunks.
func lastLinesOffset(f *os.File, n int) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}

	const chunk = 8192
	size := info.Size()
	pos := size
	found := 0
	buf := make([]byte, chunk)

	for pos > 0 {
		read := int64(chunk)
		if pos < read {
			read = pos
		}
		pos -= read
		if _, err := f.ReadAt(buf[:read], pos); err != nil && err != io.EOF {
			return 0, err
		}

		data := buf[:read]
		for i := len(data) - 1; i >= 0; i-- {
			// Skip the newline that ends the file itself.
			if data[i] == '\n' && pos+int64(i) != size-1 {
				found++
				if found == n {
					return pos + int64(i) + 1, nil
				}
			}
		}
		if bytes.Count(data, []byte{'\n'}) == 0 && pos == 0 {
			break
		}
	}
	return 0, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
