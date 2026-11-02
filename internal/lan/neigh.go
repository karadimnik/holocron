package lan

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"holocron/internal/config"
)

// Neighbour is one resolved entry of the OS neighbour table: an IPv4 address and the hardware address
// that answered for it, in the cameras.json form (aa:bb:cc:dd:ee:ff).
type Neighbour struct {
	IP  netip.Addr
	MAC string
}

// Table reads the OS neighbour table. System is the real one; FakeTable stands in for it in tests.
type Table interface {
	Read(ctx context.Context) ([]Neighbour, error)
}

// FakeTable is a fixed table.
type FakeTable struct {
	Entries []Neighbour
	Err     error
}

func (f FakeTable) Read(context.Context) ([]Neighbour, error) { return f.Entries, f.Err }

// entry is one usable table row: an IPv4 address and a unicast hardware address (no all-zero
// placeholder of an unresolved entry, no broadcast, no multicast).
func entry(ip, mac string) (Neighbour, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return Neighbour{}, false
	}
	m, ok := config.NormalizeMAC(mac)
	if !ok || m == "00:00:00:00:00:00" {
		return Neighbour{}, false
	}
	if first, _ := strconv.ParseUint(m[:2], 16, 8); first&1 == 1 { // group bit: broadcast or multicast
		return Neighbour{}, false
	}
	return Neighbour{IP: a, MAC: m}, true
}

func lines(text string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

// ParseProcNetARP reads Linux /proc/net/arp: a header line, then IP, HW type, Flags, HW address, Mask,
// Device. Only completed entries count (the ATF_COM bit, 0x2, of Flags).
func ParseProcNetARP(text string) []Neighbour {
	var out []Neighbour
	for i, l := range lines(text) {
		f := strings.Fields(l)
		if i == 0 || len(f) < 4 {
			continue
		}
		flags, err := strconv.ParseUint(strings.TrimPrefix(f[2], "0x"), 16, 32)
		if err != nil || flags&0x2 == 0 {
			continue
		}
		if n, ok := entry(f[0], f[3]); ok {
			out = append(out, n)
		}
	}
	return out
}

// ParseIPNeigh reads Linux `ip neigh show`: "<ip> dev <if> lladdr <mac> [router] [proxy] <STATE>".
// INCOMPLETE and FAILED entries are skipped (a FAILED one may still carry an old lladdr).
func ParseIPNeigh(text string) []Neighbour {
	var out []Neighbour
	for _, l := range lines(text) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch f[len(f)-1] {
		case "INCOMPLETE", "FAILED":
			continue
		}
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "lladdr" {
				if n, ok := entry(f[0], f[i+1]); ok {
					out = append(out, n)
				}
				break
			}
		}
	}
	return out
}

var (
	bsdLine = regexp.MustCompile(`\((\d{1,3}(?:\.\d{1,3}){3})\) at ([0-9A-Fa-f]{1,2}(?::[0-9A-Fa-f]{1,2}){5}) `)
	winLine = regexp.MustCompile(`^\s*(\d{1,3}(?:\.\d{1,3}){3})\s+([0-9A-Fa-f]{2}(?:-[0-9A-Fa-f]{2}){5})(?:\s|$)`)
)

// ParseBSDArp reads macOS `arp -an`: "? (<ip>) at <mac> on <if> [ifscope] [permanent] [ethernet]",
// the MAC without leading zeros per octet; "(incomplete)" stands where no MAC is known.
func ParseBSDArp(text string) []Neighbour {
	var out []Neighbour
	for _, l := range lines(text) {
		if m := bsdLine.FindStringSubmatch(l); m != nil {
			if n, ok := entry(m[1], m[2]); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// ParseWindowsArp reads Windows `arp -a`: per interface a header and rows "<ip> <aa-bb-cc-dd-ee-ff>
// <type>". Headers and the type word are localised (dynamisch, statisch), so only the address pair
// is matched.
func ParseWindowsArp(text string) []Neighbour {
	var out []Neighbour
	for _, l := range lines(text) {
		if m := winLine.FindStringSubmatch(l); m != nil {
			if n, ok := entry(m[1], m[2]); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// runTool runs one OS tool by absolute path with a fixed argv (no shell) and returns its output.
func runTool(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %v", path, strings.Join(args, " "), err)
	}
	return string(out), nil
}
