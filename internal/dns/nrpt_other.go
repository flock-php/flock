//go:build !windows

package dns

// NRPT rules exist only on Windows; macOS sends the TLD here through /etc/resolver.

// Port is unused outside Windows, where the server listens on the configured port.
const Port = 0

func SetNRPT(string) error      { return nil }
func RemoveNRPT() error         { return nil }
func NRPTInstalled(string) bool { return true }
func PolicyManaged() bool       { return false }
