package managed

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

func readSetup(path string) (string, error) {
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
