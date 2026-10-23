package host

import (
	"os"
	"runtime"
)

func CanObserve() bool {
	_, osID, version, arch, err := Metadata()
	return err == nil && osID == "debian" && version == "13" && arch == "amd64" && runtime.GOOS == "linux" && os.Geteuid() != 0
}
