//go:build !linux && !darwin && !windows

package lan

import (
	"context"
	"fmt"
	"runtime"
)

type systemRoutes struct{}

// SystemRoutes has no routing table reader on this OS: the default route stays unknown.
func SystemRoutes() Routes { return systemRoutes{} }

func (systemRoutes) Default(context.Context) ([]DefaultRoute, error) {
	return nil, fmt.Errorf("reading the routing table is not supported on %s", runtime.GOOS)
}
