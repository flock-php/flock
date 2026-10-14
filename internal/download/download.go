// Package download fetches pinned archives for the installers, verifying their SHA-256.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// File streams url to dest, verifying sha (hex SHA-256) when given; dest only appears once it is
// complete and verified. progress, when set, is called about twice a second with the bytes so far
// and the total (-1 when unknown). Large files get no overall timeout, only ctx's.
func File(ctx context.Context, client *http.Client, url, dest, sha string, progress func(done, total int64)) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	hash := sha256.New()
	var done int64
	last := time.Time{}
	buf := make([]byte, 256<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			hash.Write(buf[:n])
			done += int64(n)
			if progress != nil && time.Since(last) > 500*time.Millisecond {
				progress(done, resp.ContentLength)
				last = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			f.Close()
			return fmt.Errorf("download %s: %w", path.Base(url), readErr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}

	if sha != "" {
		if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, sha) {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", path.Base(url), got, sha)
		}
	}
	return os.Rename(tmp, dest)
}
