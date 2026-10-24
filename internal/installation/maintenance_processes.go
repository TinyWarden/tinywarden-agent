package installation

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// An acknowledged journal does not imply every descendant left its cgroup.
// Querying systemd never signals or stops a package process.
func activeMaintenanceWorkers(except string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "list-units", "--all", "--plain", "--no-legend", "--no-pager", "--type=service", "--state=active,activating,deactivating", "tinywarden-agent-maintenance-worker@*.service")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	out, e := cmd.Output()
	if e != nil || len(out) > 16384 {
		return false, errors.New("maintenance_worker_state_unknown")
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] != except {
			return true, nil
		}
	}
	return false, nil
}
