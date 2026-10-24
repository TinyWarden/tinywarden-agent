package maintenance

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// BootClock is shared by the coordinator and Python's CLOCK_BOOTTIME final gate.
// It includes suspend time and survives process changes; reboot changes BootID.
func BootClock() (string, int64, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", 0, err
	}
	boot := strings.TrimSpace(string(raw))
	var value syscall.Timespec
	_, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, 7, uintptr(unsafe.Pointer(&value)), 0)
	if errno != 0 || !uuidPattern.MatchString(boot) {
		return "", 0, errors.New("maintenance_clock_invalid")
	}
	return boot, value.Sec*1000 + value.Nsec/1000000, nil
}
func ValidateStart(start Start, job, boot string, now int64) error {
	if start.JobID != job || !uuidPattern.MatchString(job) || !uuidPattern.MatchString(start.AttemptID) || start.BootID != boot || !uuidPattern.MatchString(boot) || now < 0 || start.NotAfterMS <= now || start.NotAfterMS-now > GrantMilliseconds {
		return errors.New("maintenance_start_expired")
	}
	return nil
}
