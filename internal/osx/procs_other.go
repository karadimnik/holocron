//go:build unix && !linux && !darwin

package osx

// ListProcs is not implemented here (OSInspector cannot read image paths either).
func ListProcs() ([]ProcEntry, error) { return nil, errNoInspect }
