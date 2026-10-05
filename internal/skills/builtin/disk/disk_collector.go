package disk

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

const maxMountInfo = 4 << 20
const MaxCollectorOutput = 1 << 20
const oPath = 0x200000 // Linux O_PATH is absent from syscall on some Go releases.

type DiskMount struct {
	MountID        int     `json:"mount_id"`
	MountPath      string  `json:"mount_path"`
	MountRoot      string  `json:"mount_root"`
	FilesystemType string  `json:"filesystem_type"`
	Kind           string  `json:"kind"`
	Writable       bool    `json:"writable"`
	SharedCapacity bool    `json:"shared_capacity"`
	Reason         string  `json:"reason"`
	TotalBytes     *string `json:"total_bytes"`
	FreeBytes      *string `json:"free_bytes"`
	AvailableBytes *string `json:"available_bytes"`
}

type CollectorResult struct {
	Coverage       string      `json:"coverage"`
	Reason         string      `json:"reason"`
	ExcludedKernel int         `json:"excluded_kernel"`
	ExcludedRemote int         `json:"excluded_remote"`
	Mounts         []DiskMount `json:"mounts"`
}

type mountEntry struct {
	id             int
	dev            string
	root, path, fs string
	writable       bool
}

var kernelTypes = map[string]bool{"proc": true, "sysfs": true, "devtmpfs": true,
	"devpts": true, "cgroup": true, "cgroup2": true, "securityfs": true,
	"debugfs": true, "tracefs": true, "configfs": true, "pstore": true,
	"mqueue": true, "hugetlbfs": true, "ramfs": true, "autofs": true,
	"binfmt_misc": true, "fusectl": true, "rpc_pipefs": true,
	"efivarfs": true, "nsfs": true, "bpf": true}
var remoteTypes = map[string]bool{"nfs": true, "nfs4": true, "cifs": true,
	"smb3": true, "ceph": true, "9p": true, "afs": true, "coda": true,
	"glusterfs": true, "fuse.sshfs": true}
var localTypes = map[string]bool{"ext2": true, "ext3": true, "ext4": true,
	"xfs": true, "btrfs": true, "zfs": true, "f2fs": true, "vfat": true,
	"exfat": true, "ntfs3": true, "jfs": true, "reiserfs": true,
	"udf": true, "iso9660": true, "squashfs": true, "erofs": true, "tmpfs": true}

func unescapeMount(value string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) || value[i+1] < '0' || value[i+1] > '7' ||
			value[i+2] < '0' || value[i+2] > '7' || value[i+3] < '0' || value[i+3] > '7' {
			return "", false
		}
		code, _ := strconv.ParseUint(value[i+1:i+4], 8, 8)
		out.WriteByte(byte(code))
		i += 3
	}
	result := out.String()
	if !utf8.ValidString(result) || len(result) < 1 || len(result) > 1024 {
		return "", false
	}
	for _, char := range result {
		if char < 0x20 || char == 0x7f {
			return "", false
		}
	}
	return result, true
}

func parseMountInfo(data []byte) ([]mountEntry, string) {
	if len(data) > maxMountInfo {
		return nil, "inventory_overflow"
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) > 4096 {
		return nil, "inventory_overflow"
	}
	entries := make([]mountEntry, 0, len(lines))
	seen := make(map[int]bool)
	for _, line := range lines {
		parts := strings.Fields(string(line))
		separator := -1
		for i, part := range parts {
			if part == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(parts) < separator+4 {
			return nil, "inventory_malformed"
		}
		id, err := strconv.Atoi(parts[0])
		if err != nil || id <= 0 || id > 2147483647 || seen[id] {
			return nil, "inventory_malformed"
		}
		seen[id] = true
		root, okRoot := unescapeMount(parts[3])
		path, okPath := unescapeMount(parts[4])
		if !okRoot || !okPath || !strings.HasPrefix(path, "/") ||
			!strings.HasPrefix(root, "/") {
			return nil, "inventory_malformed"
		}
		fs := parts[separator+1]
		if len(fs) < 1 || len(fs) > 64 {
			return nil, "inventory_malformed"
		}
		for _, char := range fs {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
				char >= '0' && char <= '9' || strings.ContainsRune("._+-", char)) {
				return nil, "inventory_malformed"
			}
		}
		writable := false
		for _, option := range strings.Split(parts[5], ",") {
			if option == "rw" {
				writable = true
			}
		}
		entries = append(entries, mountEntry{id: id, dev: parts[2], root: root,
			path: path, fs: fs, writable: writable})
	}
	return entries, "none"
}

func readMountInfo() ([]byte, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMountInfo+1))
	return data, err
}

func mountIDForFD(fd int) (int, error) {
	file, err := os.Open("/proc/self/fdinfo/" + strconv.Itoa(fd))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	for scan.Scan() {
		value, ok := strings.CutPrefix(scan.Text(), "mnt_id:\t")
		if ok {
			return strconv.Atoi(value)
		}
	}
	return 0, errors.New("missing mount id")
}

func measureMount(entry mountEntry, shared bool) DiskMount {
	m := DiskMount{MountID: entry.id, MountPath: entry.path, MountRoot: entry.root,
		FilesystemType: entry.fs, Kind: "local", Writable: entry.writable,
		SharedCapacity: shared, Reason: "mount_unverifiable"}
	fd, err := syscall.Open(entry.path, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			m.Reason = "mount_disappeared"
		} else {
			m.Reason = "mount_inaccessible"
		}
		return m
	}
	defer syscall.Close(fd)
	var target syscall.Stat_t
	if syscall.Fstat(fd, &target) != nil || target.Mode&syscall.S_IFMT == syscall.S_IFLNK {
		return m
	}
	actual, err := mountIDForFD(fd)
	if err != nil || actual != entry.id {
		return m
	}
	var stat syscall.Statfs_t
	if syscall.Fstatfs(fd, &stat) != nil {
		m.Reason = "mount_inaccessible"
		return m
	}
	if stat.Frsize < 0 || stat.Bsize < 0 {
		return m
	}
	fragment := uint64(stat.Frsize)
	if fragment == 0 {
		fragment = uint64(stat.Bsize)
	}
	if fragment == 0 || stat.Blocks > ^uint64(0)/fragment ||
		stat.Bfree > ^uint64(0)/fragment || stat.Bavail > ^uint64(0)/fragment ||
		stat.Bfree > stat.Blocks || stat.Bavail > stat.Bfree {
		return m
	}
	total, free, available := stat.Blocks*fragment, stat.Bfree*fragment, stat.Bavail*fragment
	if total-free+available == 0 {
		return m
	}
	a, b, c := strconv.FormatUint(total, 10), strconv.FormatUint(free, 10), strconv.FormatUint(available, 10)
	m.TotalBytes, m.FreeBytes, m.AvailableBytes = &a, &b, &c
	m.Reason = "none"
	return m
}

func collectDisk(read func() ([]byte, error), measure func(mountEntry, bool) DiskMount) CollectorResult {
	result := CollectorResult{Coverage: "complete", Reason: "none", Mounts: []DiskMount{}}
	before, err := read()
	if err != nil {
		result.Coverage, result.Reason = "incomplete", "inventory_unavailable"
		return result
	}
	entries, reason := parseMountInfo(before)
	if reason != "none" {
		result.Coverage, result.Reason = "incomplete", reason
		return result
	}
	localCounts := map[string]int{}
	for _, entry := range entries {
		if localTypes[entry.fs] {
			localCounts[entry.dev]++
		}
	}
	for _, entry := range entries {
		if kernelTypes[entry.fs] {
			result.ExcludedKernel++
			continue
		}
		if remoteTypes[entry.fs] {
			result.ExcludedRemote++
			continue
		}
		if len(result.Mounts) == 128 {
			result.Coverage, result.Reason = "incomplete", "records_overflow"
			break
		}
		if !localTypes[entry.fs] {
			result.Mounts = append(result.Mounts, DiskMount{MountID: entry.id, MountPath: entry.path,
				MountRoot: entry.root, FilesystemType: entry.fs, Kind: "unsupported",
				Writable: entry.writable, Reason: "unsupported_type"})
			if result.Reason == "none" {
				result.Coverage, result.Reason = "incomplete", "unsupported_type"
			}
			continue
		}
		mount := measure(entry, localCounts[entry.dev] > 1)
		result.Mounts = append(result.Mounts, mount)
		if mount.Reason != "none" && result.Reason == "none" {
			result.Coverage, result.Reason = "incomplete", mount.Reason
		}
	}
	if result.ExcludedKernel > 4096 {
		result.ExcludedKernel = 4096
	}
	if result.ExcludedRemote > 4096 {
		result.ExcludedRemote = 4096
	}
	after, err := read()
	if err != nil || !bytes.Equal(before, after) {
		result.Coverage, result.Reason = "incomplete", "topology_changed"
	}
	// Canonical wire order is the numerical mount ID, not mountinfo presentation order.
	for i := 1; i < len(result.Mounts); i++ {
		for j := i; j > 0 && result.Mounts[j].MountID < result.Mounts[j-1].MountID; j-- {
			result.Mounts[j], result.Mounts[j-1] = result.Mounts[j-1], result.Mounts[j]
		}
	}
	return result
}

func CollectDisk(out io.Writer) error {
	result := collectDisk(readMountInfo, measureMount)
	return json.NewEncoder(out).Encode(result)
}
