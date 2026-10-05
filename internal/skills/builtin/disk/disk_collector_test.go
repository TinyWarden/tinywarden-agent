package disk

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func mountLine(id int, device, root, path, options, fs string) string {
	return fmt.Sprintf("%d 1 %s %s %s %s - %s ignored rw\n", id, device, root, path, options, fs)
}

func TestDiskCollectorMountCoverage(t *testing.T) {
	inventory := mountLine(10, "8:1", "/", "/", "rw,relatime", "ext4") +
		mountLine(11, "8:1", "/var", "/bind\\040path", "ro,relatime", "ext4") +
		mountLine(12, "0:4", "/", "/run", "rw", "tmpfs") +
		mountLine(13, "0:5", "/", "/proc", "rw", "proc") +
		mountLine(14, "0:6", "/", "/remote", "rw", "nfs4") +
		mountLine(15, "0:7", "/", "/mystery", "rw", "fuse.other")
	measure := func(entry mountEntry, shared bool) DiskMount {
		m := DiskMount{MountID: entry.id, MountPath: entry.path, MountRoot: entry.root,
			FilesystemType: entry.fs, Kind: "local", Writable: entry.writable,
			SharedCapacity: shared, Reason: "none"}
		a, b, c := "100", "20", "10"
		m.TotalBytes, m.FreeBytes, m.AvailableBytes = &a, &b, &c
		return m
	}
	result := collectDisk(func() ([]byte, error) { return []byte(inventory), nil }, measure)
	if result.Coverage != "incomplete" || result.Reason != "unsupported_type" ||
		result.ExcludedKernel != 1 || result.ExcludedRemote != 1 || len(result.Mounts) != 4 {
		t.Fatalf("coverage %+v", result)
	}
	if result.Mounts[1].MountPath != "/bind path" || result.Mounts[1].Writable ||
		!result.Mounts[0].SharedCapacity || !result.Mounts[1].SharedCapacity ||
		result.Mounts[2].FilesystemType != "tmpfs" || result.Mounts[3].Kind != "unsupported" {
		t.Fatalf("mount roles %+v", result.Mounts)
	}
	if _, reason := parseMountInfo([]byte(mountLine(1, "8:1", "/", "/bad\\377", "rw", "ext4"))); reason != "inventory_malformed" {
		t.Fatal("invalid UTF-8 path was accepted")
	}
	if _, reason := parseMountInfo(bytes.Repeat([]byte(mountLine(1, "8:1", "/", "/", "rw", "ext4")), 4097)); reason != "inventory_overflow" {
		t.Fatal("inventory bound was ignored")
	}
	changed := 0
	topology := collectDisk(func() ([]byte, error) {
		changed++
		if changed == 1 {
			return []byte(inventory), nil
		}
		return []byte(inventory + mountLine(20, "8:2", "/", "/new", "rw", "xfs")), nil
	}, measure)
	if topology.Coverage != "incomplete" || topology.Reason != "topology_changed" {
		t.Fatalf("topology change %+v", topology)
	}
	tooMany := strings.Builder{}
	for i := 1; i <= 129; i++ {
		tooMany.WriteString(mountLine(i, "8:1", "/", fmt.Sprintf("/m%d", i), "rw", "ext4"))
	}
	overflow := collectDisk(func() ([]byte, error) { return []byte(tooMany.String()), nil }, measure)
	if overflow.Reason != "records_overflow" || len(overflow.Mounts) != 128 {
		t.Fatal("record cap failed")
	}
	unavailable := collectDisk(func() ([]byte, error) { return nil, os.ErrPermission }, measure)
	if unavailable.Reason != "inventory_unavailable" {
		t.Fatal("unavailable inventory was healthy")
	}
	missing := collectDisk(func() ([]byte, error) { return []byte(mountLine(1, "8:1", "/", "/missing", "rw", "ext4")), nil },
		func(entry mountEntry, shared bool) DiskMount { return measureMount(entry, shared) })
	if missing.Reason != "mount_disappeared" {
		t.Fatalf("missing mount %+v", missing)
	}
	if os.Geteuid() != 0 {
		parent := t.TempDir()
		secret := parent + "/secret"
		if err := os.Mkdir(secret, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(secret, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(secret, 0700)
		inaccessible := measureMount(mountEntry{id: 1, path: secret + "/mount", fs: "ext4"}, false)
		if inaccessible.Reason != "mount_inaccessible" {
			t.Fatalf("inaccessible mount %+v", inaccessible)
		}
	}
}
