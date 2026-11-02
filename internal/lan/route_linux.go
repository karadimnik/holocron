package lan

import (
	"context"
	"os"
)

type systemRoutes struct{}

// SystemRoutes is the OS routing table: /proc/net/route.
func SystemRoutes() Routes { return systemRoutes{} }

func (systemRoutes) Default(context.Context) ([]DefaultRoute, error) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	return ParseProcNetRoute(string(b)), nil
}
