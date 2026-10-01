package runner

import (
	"debug/elf"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func platformAllowed() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(data) > 8192 {
		return false
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			if _, duplicate := values[key]; duplicate {
				return false
			}
			values[key] = strings.Trim(value, "\"'")
		}
	}
	return values["ID"] == "debian" && values["VERSION_ID"] == "13"
}

func identityAllowed(data []byte) bool {
	values := make(map[string][]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			if _, duplicate := values[key]; duplicate {
				return false
			}
			values[key] = strings.Fields(value)
		}
	}
	for _, key := range []string{"Uid", "Gid"} {
		v := values[key]
		if len(v) != 4 {
			return false
		}
		first, err := strconv.ParseUint(v[0], 10, 32)
		if err != nil || first == 0 {
			return false
		}
		for _, value := range v[1:] {
			n, err := strconv.ParseUint(value, 10, 32)
			if err != nil || n != first {
				return false
			}
		}
	}
	for _, key := range []string{"CapPrm", "CapEff", "CapInh", "CapAmb"} {
		v := values[key]
		if len(v) != 1 {
			return false
		}
		n, err := strconv.ParseUint(v[0], 16, 64)
		if err != nil || n != 0 {
			return false
		}
	}
	return true
}

func nativeTrusted(path string) bool {
	return checkNative(path, os.Lstat, func(p string) (int, error) { return syscall.Getxattr(p, "security.capability", nil) })
}

func checkNative(path string, stat func(string) (os.FileInfo, error), capabilities func(string) (int, error)) bool {
	for p := path; ; p = filepath.Dir(p) {
		info, err := stat(p)
		if err != nil || !trustedMode(info, p == path) {
			return false
		}
		if p == "/" {
			break
		}
	}
	n, err := capabilities(path)
	if (err != nil && err != syscall.ENODATA) || (err == nil && n != 0) {
		return false
	}
	f, err := elf.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	return f.Class == elf.ELFCLASS64 && f.Data == elf.ELFDATA2LSB &&
		f.Machine == elf.EM_X86_64 && (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN)
}

func trustedMode(info os.FileInfo, executable bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 ||
		info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	if executable {
		return info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
	}
	return info.IsDir()
}

// Called only on the supervisor's locked thread, immediately before exec.
func prepareCommand(path string) error {
	status, err := os.ReadFile("/proc/thread-self/status")
	if err != nil || !platformAllowed() || !identityAllowed(status) || !nativeTrusted(path) {
		return errPolicy
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 38, 1, 0, 0, 0, 0) // PR_SET_NO_NEW_PRIVS
	if errno != 0 {
		return errPolicy
	}
	value, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 39, 0, 0, 0, 0, 0) // PR_GET_NO_NEW_PRIVS
	if errno != 0 || value != 1 {
		return errPolicy
	}
	return sealDescriptors()
}

func sealDescriptors() error {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return errPolicy
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			return errPolicy
		}
		if fd <= 2 {
			continue
		}
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno == syscall.EBADF {
			continue
		} // The directory reader already closed.
		if errno != 0 {
			return errPolicy
		}
		_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, flags|syscall.FD_CLOEXEC)
		if errno != 0 && errno != syscall.EBADF {
			return errPolicy
		}
	}
	return nil
}

func cleanEnvironment() []string {
	return []string{"LANG=C", "LC_ALL=C", "TZ=UTC", "PATH=/usr/bin:/bin",
		"HOME=/nonexistent", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0", "SYSTEMD_URLIFY=0"}
}
