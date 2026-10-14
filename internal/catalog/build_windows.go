//go:build windows

package catalog

import (
	"context"
	"errors"

	"flock/internal/components"
)

var errNoBuilds = errors.New("building from source is not supported on Windows")

func buildRedis(context.Context, string, string, *components.Reporter) error   { return errNoBuilds }
func buildMariaDB(context.Context, string, string, *components.Reporter) error { return errNoBuilds }
