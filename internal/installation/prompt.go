package installation

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func readSetup(path string, input io.Reader) (string, error) {
	if input != nil {
		if path != "" {
			return "", errors.New("invalid_setup_input_options")
		}
		// One code, optionally newline-terminated. Never fall back to a terminal
		// after an explicitly selected input fails or exceeds the protocol limit.
		b, e := io.ReadAll(io.LimitReader(input, 4099))
		code := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if e != nil || len(code) == 0 || len(code) > 4096 || strings.ContainsAny(code, "\r\n") {
			return "", errors.New("invalid_setup_code")
		}
		return code, nil
	}
	if path != "" {
		fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if e != nil {
			return "", e
		}
		f := os.NewFile(uintptr(fd), path)
		defer f.Close()
		i, e := f.Stat()
		if e != nil || !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || i.Size() > 4096 || i.Sys().(*syscall.Stat_t).Uid != 0 {
			return "", errors.New("setup code file must be a root-owned regular file with mode 0600")
		}
		b, e := io.ReadAll(io.LimitReader(f, 4097))
		return string(b), e
	}
	f, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if e != nil {
		return "", errors.New("use a terminal or a protected --setup-code-file")
	}
	defer f.Close()
	var original syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&original))); errno != 0 {
		return "", errno
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&hidden))); errno != 0 {
		return "", errno
	}
	defer syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&original)))
	fmt.Fprint(f, "Paste setup code (hidden): ")
	b, e := bufio.NewReader(io.LimitReader(f, 4097)).ReadString('\n')
	fmt.Fprintln(f)
	if e != nil || len(b) > 4096 {
		return "", errors.New("invalid setup code input")
	}
	return b, nil
}
