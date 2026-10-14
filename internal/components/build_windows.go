//go:build windows

package components

import (
	"context"
	"errors"
)

var errNoBuilds = errors.New("building from source is not supported on Windows; Flock downloads the components instead")

// BuildValkey is not available on Windows, which has no Valkey build.
func BuildValkey(context.Context, Sources, string, string, *Reporter) (string, func(), error) {
	return "", nil, errNoBuilds
}

// BuildPostgresClient is not available on Windows, where the client tools are downloaded.
func BuildPostgresClient(context.Context, Sources, string, string, *Reporter) (string, func(), error) {
	return "", nil, errNoBuilds
}
