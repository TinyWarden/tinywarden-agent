package installation

import (
	"fmt"
	"strings"
)

// Package names describe the supported Debian host, not executable names.
func programPrerequisites(available func(string) bool) error {
	requirements := []struct{ path, pkg string }{
		{"/usr/bin/python3.13", "python3"},
		{"/usr/bin/bwrap", "bubblewrap"},
		{"/usr/bin/openssl", "openssl"},
		{"/usr/bin/systemctl", "systemd"},
		{"/usr/bin/getent", "libc-bin"},
		{"/usr/sbin/useradd", "passwd"},
		{"/usr/sbin/runuser", "util-linux"},
	}
	var missing, packages []string
	for _, requirement := range requirements {
		if !available(requirement.path) {
			missing = append(missing, requirement.path)
			packages = append(packages, requirement.pkg)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing prerequisites: %s; install missing packages: apt install %s", strings.Join(missing, ", "), strings.Join(packages, " "))
}
