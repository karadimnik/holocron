package lan

import (
	"context"
	"os"
	"path/filepath"
)

type systemRoutes struct{}

// SystemRoutes is the OS routing table: %SystemRoot%\System32\route.exe print -4.
func SystemRoutes() Routes { return systemRoutes{} }

func (systemRoutes) Default(ctx context.Context) ([]DefaultRoute, error) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	out, err := runTool(ctx, filepath.Join(systemRoot, "System32", "route.exe"), "print", "-4")
	if err != nil {
		return nil, err
	}
	return ParseWindowsRoutePrint(out), nil
}
