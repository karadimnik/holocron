//go:build !linux && !darwin && !windows

package lan

import (
	"context"
	"fmt"
	"runtime"
)

type system struct{}

// System has no neighbour table reader on this OS: MACs stay unknown.
func System() Table { return system{} }

func (system) Read(context.Context) ([]Neighbour, error) {
	return nil, fmt.Errorf("reading the neighbour table is not supported on %s", runtime.GOOS)
}
