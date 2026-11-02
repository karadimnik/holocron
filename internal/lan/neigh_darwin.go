package lan

import "context"

type system struct{}

// System is the OS neighbour table: /usr/sbin/arp -an.
func System() Table { return system{} }

func (system) Read(ctx context.Context) ([]Neighbour, error) {
	out, err := runTool(ctx, "/usr/sbin/arp", "-an")
	if err != nil {
		return nil, err
	}
	return ParseBSDArp(out), nil
}
