package lan

import (
	"context"
	"errors"
	"os"
)

type system struct{}

// System is the OS neighbour table: /proc/net/arp, or `ip neigh show` when that cannot be read.
func System() Table { return system{} }

func (system) Read(ctx context.Context) ([]Neighbour, error) {
	b, err := os.ReadFile("/proc/net/arp")
	if err == nil {
		return ParseProcNetARP(string(b)), nil
	}
	for _, ip := range []string{"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip", "/bin/ip"} {
		if _, serr := os.Stat(ip); serr == nil {
			out, err := runTool(ctx, ip, "neigh", "show")
			if err != nil {
				return nil, err
			}
			return ParseIPNeigh(out), nil
		}
	}
	return nil, errors.Join(err, errors.New("no ip command found"))
}
