"""Native-architecture BPF allowlist exported by distro libseccomp."""
import ctypes
import errno
import os
import platform

ALLOW = 0x7FFF0000
DENY = 0x00050000 | errno.EPERM
# No sockets, device ioctls, ptrace, mount, unshare, setns, BPF or kernel mutation calls.
SYSCALLS = """read write readv writev pread64 pwrite64 close close_range fstat newfstatat
stat lstat statx lseek open openat access faccessat faccessat2 getdents getdents64
readlink readlinkat mmap mprotect munmap mremap madvise brk rt_sigaction rt_sigprocmask
rt_sigreturn sigaltstack futex futex_waitv set_tid_address set_robust_list rseq
arch_prctl prlimit64 getrlimit setrlimit clock_gettime clock_nanosleep nanosleep
gettimeofday time getrandom getpid getppid gettid getuid geteuid getgid getegid
getresuid getresgid getgroups uname getcwd chdir fchdir umask fcntl flock dup dup2 dup3
pipe pipe2 select pselect6 poll ppoll epoll_create1 epoll_ctl epoll_wait epoll_pwait
epoll_pwait2 exit exit_group execve execveat wait4 waitid kill tkill tgkill sched_yield
sched_getaffinity sched_getparam sched_getscheduler sched_get_priority_max
sched_get_priority_min sched_setscheduler sched_setparam getpriority setpriority
getrusage times sysinfo statfs fstatfs mkdir mkdirat unlink unlinkat rename renameat
renameat2 chmod fchmod fchmodat truncate ftruncate fsync fdatasync utimensat
sendfile copy_file_range fadvise64 capget""".split()


class Comparison(ctypes.Structure):
    _fields_ = [("arg", ctypes.c_uint), ("op", ctypes.c_int),
                ("a", ctypes.c_uint64), ("b", ctypes.c_uint64)]


def policy_fd():
    if platform.machine() not in {"x86_64", "aarch64"}:
        raise RuntimeError("runtime_unavailable")
    lib = ctypes.CDLL("libseccomp.so.2", use_errno=True)
    lib.seccomp_init.argtypes = [ctypes.c_uint32]
    lib.seccomp_init.restype = ctypes.c_void_p
    lib.seccomp_release.argtypes = [ctypes.c_void_p]
    lib.seccomp_syscall_resolve_name.argtypes = [ctypes.c_char_p]
    lib.seccomp_rule_add_array.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_int,
                                         ctypes.c_uint, ctypes.POINTER(Comparison)]
    lib.seccomp_export_bpf.argtypes = [ctypes.c_void_p, ctypes.c_int]
    context = lib.seccomp_init(DENY)
    if not context:
        raise RuntimeError("runtime_unavailable")
    fd = os.memfd_create("tw-seccomp-v1", os.MFD_CLOEXEC)
    try:
        def add(name, action=ALLOW, comparison=None):
            call = lib.seccomp_syscall_resolve_name(name.encode())
            if call < 0:
                return
            ptr = ctypes.pointer(comparison) if comparison is not None else None
            if lib.seccomp_rule_add_array(context, action, call, int(ptr is not None), ptr) != 0:
                raise RuntimeError("runtime_unavailable")
        for name in SYSCALLS:
            add(name)
        # glibc fopen sets close-on-exec using FIOCLEX. Permit that descriptor-only
        # operation without exposing terminal, network or device ioctl requests.
        add("ioctl", comparison=Comparison(1, 4, 0x5451, 0))
        # clone3's argument pointer cannot be inspected by classic BPF; force libc fallback.
        add("clone3", 0x00050000 | errno.ENOSYS)
        # Reject all namespace flags, including CLONE_NEWCGROUP and CLONE_NEWTIME.
        add("clone", comparison=Comparison(0, 7, 0x7E020080, 0))
        add("fork")
        add("vfork")
        if lib.seccomp_export_bpf(context, fd) != 0:
            raise RuntimeError("runtime_unavailable")
        os.lseek(fd, 0, os.SEEK_SET)
        return fd
    except BaseException:
        os.close(fd)
        raise
    finally:
        lib.seccomp_release(context)
