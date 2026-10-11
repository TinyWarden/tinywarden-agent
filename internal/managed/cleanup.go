package managed

import (
	"os"
	"path/filepath"
	"strings"
)

func cleanup(base, active string) error {
	releases, e := os.ReadDir(filepath.Join(base, "releases"))
	if e != nil {
		return e
	}
	processes, e := os.ReadDir("/proc")
	if e != nil {
		return e
	}
	for _, r := range releases {
		if r.Name() == active || !validTarget(r.Name()) || !r.IsDir() {
			continue
		}
		dir := filepath.Join(base, "releases", r.Name())
		busy := false
		for _, p := range processes {
			if !p.IsDir() {
				continue
			}
			exe, _ := os.Readlink(filepath.Join("/proc", p.Name(), "exe"))
			if strings.HasPrefix(exe, dir+"/") {
				busy = true
				break
			}
		}
		if !busy {
			if e = os.RemoveAll(dir); e != nil {
				return e
			}
		}
	}
	return syncDirectory(filepath.Join(base, "releases"))
}
