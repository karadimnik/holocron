package lan

import "context"

type systemRoutes struct{}

// SystemRoutes is the OS routing table: /usr/sbin/netstat -rn -f inet, else /sbin/route -n get default.
func SystemRoutes() Routes { return systemRoutes{} }

func (systemRoutes) Default(ctx context.Context) ([]DefaultRoute, error) {
	out, err := runTool(ctx, "/usr/sbin/netstat", "-rn", "-f", "inet")
	if err == nil {
		if rs := ParseNetstatInet(out); len(rs) > 0 {
			return rs, nil
		}
	}
	out, rerr := runTool(ctx, "/sbin/route", "-n", "get", "default")
	if rerr != nil {
		return nil, rerr
	}
	return ParseRouteGet(out), nil
}
