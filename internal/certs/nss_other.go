//go:build !linux

package certs

// TrustInBrowsers: browsers on macOS and Windows use the system's trust store.
func TrustInBrowsers(string) error { return nil }
