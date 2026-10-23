package runner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const plainIdentity = "Uid:\t1020 1020 1020 1020\nGid:\t1020 1020 1020 1020\nCapPrm:\t00000000\nCapEff:\t00000000\nCapInh:\t00000000\nCapAmb:\t00000000\n"

func TestUnprivilegedIdentity(t *testing.T) {
	if !identityAllowed([]byte(plainIdentity)) {
		t.Fatal("ordinary identity rejected")
	}
	for _, bad := range []string{
		strings.Replace(plainIdentity, "1020 1020 1020 1020", "0 0 0 0", 1),
		strings.Replace(plainIdentity, "Gid:\t1020 1020 1020 1020", "Gid:\t0 0 0 0", 1),
		strings.Replace(plainIdentity, "1020 1020 1020 1020", "1020 1020 0 1020", 1),
		strings.Replace(plainIdentity, "1020 1020 1020 1020", "1020 1021 1020 1020", 1),
		strings.Replace(plainIdentity, "CapAmb:\t00000000\n", "", 1),
		plainIdentity + "Uid:\t1020 1020 1020 1020\n",
	} {
		if identityAllowed([]byte(bad)) {
			t.Fatal("unsafe or unverifiable identity accepted")
		}
	}
	for _, key := range []string{"CapPrm", "CapEff", "CapInh", "CapAmb"} {
		bad := strings.Replace(plainIdentity, key+":\t00000000", key+":\t00000001", 1)
		if identityAllowed([]byte(bad)) {
			t.Fatalf("%s accepted", key)
		}
	}
}

type syntheticInfo struct {
	mode os.FileMode
	uid  uint32
}

func (f syntheticInfo) Name() string       { return "tool" }
func (f syntheticInfo) Size() int64        { return 0 }
func (f syntheticInfo) Mode() os.FileMode  { return f.mode }
func (f syntheticInfo) ModTime() time.Time { return time.Time{} }
func (f syntheticInfo) IsDir() bool        { return f.mode.IsDir() }
func (f syntheticInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }

func TestExecutableAndAncestorTrust(t *testing.T) {
	if !nativeTrusted("/usr/bin/test") {
		t.Fatal("supported native tool rejected")
	}
	for _, path := range []string{"/bin/test", "/usr/bin/ldd", "/usr/bin/passwd", "/usr/bin/tinywarden-no-such-file"} {
		if nativeTrusted(path) {
			t.Fatalf("unsafe/unavailable path %s accepted", path)
		}
	}
	for _, bad := range []syntheticInfo{{0755, 1020}, {0775, 0}, {0757, 0}, {0644, 0}, {0755 | os.ModeSymlink, 0}, {0755 | os.ModeSetuid, 0}, {0755 | os.ModeSetgid, 0}, {0755 | os.ModeNamedPipe, 0}} {
		if trustedMode(bad, true) {
			t.Fatalf("unsafe executable mode/owner accepted: %+v", bad)
		}
	}
	for _, p := range []string{"/", "/usr", "/usr/bin"} {
		stat := func(path string) (os.FileInfo, error) {
			if path == p {
				return syntheticInfo{os.ModeDir | 0775, 0}, nil
			}
			return os.Lstat(path)
		}
		if checkNative("/usr/bin/test", stat, func(string) (int, error) { return 0, syscall.ENODATA }) {
			t.Fatalf("writable ancestor %s accepted", p)
		}
	}
	for _, probe := range []func(string) (int, error){
		func(string) (int, error) { return 20, nil },
		func(string) (int, error) { return 0, syscall.EACCES },
		func(string) (int, error) { return 0, syscall.ENOTSUP },
	} {
		if checkNative("/usr/bin/test", os.Lstat, probe) {
			t.Fatal("capability-bearing/unverifiable tool accepted")
		}
	}
	// A magic prefix alone does not establish a native executable. Model trusted
	// metadata without granting root access to write a real installed tool.
	for _, data := range [][]byte{{0x7f, 'E', 'L', 'F'}, foreignELFHeader()} {
		path := t.TempDir() + "/synthetic-tool"
		if os.WriteFile(path, data, 0700) != nil {
			t.Fatal("fixture write failed")
		}
		stat := func(p string) (os.FileInfo, error) {
			if p == path {
				return syntheticInfo{0755, 0}, nil
			}
			return syntheticInfo{os.ModeDir | 0755, 0}, nil
		}
		if checkNative(path, stat, func(string) (int, error) { return 0, syscall.ENODATA }) {
			t.Fatal("malformed/foreign ELF accepted as native")
		}
	}
}

func foreignELFHeader() []byte {
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(data[16:], 2)   // ET_EXEC
	binary.LittleEndian.PutUint16(data[18:], 183) // EM_AARCH64
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint16(data[52:], 64)
	return data
}

func TestRealNativeToolAndCleanCommandContext(t *testing.T) {
	result := testEngine("native").execute(context.Background(), recipeBytes(1, 3))
	if result.Outcome != Exited || len(result.Steps) != 1 || result.Steps[0].ExitCode == nil || !result.Steps[0].CleanupComplete {
		t.Fatalf("native tool failed: outcome=%s steps=%v", result.Outcome, result.Steps)
	}
	f, err := os.CreateTemp(t.TempDir(), "synthetic-parent-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Deliberately clear CLOEXEC on an owned descriptor. os/exec must still not
	// pass it through to either the supervisor or its command.
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_SETFD, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	t.Setenv("TINYWARDEN_SYNTHETIC_SECRET", "synthetic-secret")
	t.Setenv("APT_CONFIG", f.Name())
	t.Setenv("HTTP_PROXY", "synthetic-secret")
	result = testEngine("inspect:"+f.Name()).execute(context.Background(), recipeBytes(1, 3))
	if result.Outcome != Exited {
		t.Fatalf("inspect: %s", result.Outcome)
	}
	var info inspection
	if json.Unmarshal(result.Steps[0].Stdout, &info) != nil {
		t.Fatal("missing inspection")
	}
	wanted := cleanEnvironment()
	slices.Sort(wanted)
	slices.Sort(info.Environment)
	if !slices.Equal(wanted, info.Environment) || info.Directory != "/" || !info.NullInput || !info.NoNewPrivileges || info.LeakedDescriptor {
		t.Fatalf("unsafe context: %+v", info)
	}
	for _, mode := range []string{"policy", "spawn"} {
		result = testEngine(mode).execute(context.Background(), recipeBytes(2, 3))
		wanted := PolicyRejected
		if mode == "spawn" {
			wanted = SpawnFailed
		}
		if result.Outcome != wanted || len(result.Steps) != 1 || result.Steps[0].ExitCode != nil {
			t.Fatalf("%s: %+v", mode, result)
		}
	}
	e := testEngine("success")
	e.platform = func() bool { return false }
	if r := e.execute(context.Background(), recipeBytes(1, 3)); r.Outcome != PolicyRejected || len(r.Steps) != 0 {
		t.Fatal("unsupported platform launched")
	}
}
