package lan

import (
	"context"
	"os"
	"path/filepath"
)

type system struct{}

// System is the OS neighbour table: %SystemRoot%\System32\ARP.EXE -a.
func System() Table { return system{} }

func (system) Read(ctx context.Context) ([]Neighbour, error) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	out, err := runTool(ctx, filepath.Join(systemRoot, "System32", "ARP.EXE"), "-a")
	if err != nil {
		return nil, err
	}
	return ParseWindowsArp(out), nil
}
