package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Info tells native apps where the daemon's HTTP API is and how to authenticate.
type Info struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// InfoFile is <root>/api.json.
func InfoFile(root string) string { return filepath.Join(root, "api.json") }

// WriteInfo generates a fresh token, writes api.json readable only by the user, and returns the token.
func WriteInfo(root, url string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	data, err := json.MarshalIndent(Info{URL: url, Token: token}, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := InfoFile(root) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	return token, os.Rename(tmp, InfoFile(root))
}
