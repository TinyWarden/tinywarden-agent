"""Trusted Debian adapter support. Embedded in the signed executable, not SDK code."""
import contextlib
from collections import deque
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import signal
import threading
import time

import apt
import apt.progress.base
import apt_pkg

POLICY = {'version': 1, 'new_packages': False, 'removals': False, 'downgrades': False,
          'holds': 'preserve', 'conffiles': 'keep-local', 'needrestart': 'list-only'}
EFFECT = 'none'
MAX_ROWS = 5000
MAX_PLAN_BYTES = 4 * 1024 * 1024

class MaintenanceError(Exception):
    def __init__(self, reason, subjects=()):
        super().__init__(reason)
        self.subject_count = len(subjects)
        self.subjects = list(subjects)[:MAX_ROWS]

def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()

def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()

def open_input(path, directory=False):
    # Validate every original and symlink-target component from an anchored root.
    # A root-owned leaf inside an unprivileged writable directory is not trusted.
    path = Path(path)
    if not path.is_absolute():
        raise MaintenanceError('maintenance_input_invalid')
    pending, links = deque(path.parts[1:]), 0
    stack = [os.open('/', os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)]
    try:
        root = os.fstat(stack[0])
        if root.st_uid != 0 or root.st_mode & 0o022:
            raise MaintenanceError('maintenance_input_invalid')
        while pending:
            name = pending.popleft()
            if name in ('', '.'):
                continue
            if name == '..':
                if len(stack) > 1:
                    os.close(stack.pop())
                continue
            info = os.stat(name, dir_fd=stack[-1], follow_symlinks=False)
            if stat.S_ISLNK(info.st_mode):
                links += 1
                if info.st_uid != 0 or links > 40:
                    raise MaintenanceError('maintenance_input_invalid')
                target = Path(os.readlink(name, dir_fd=stack[-1]))
                if target.is_absolute():
                    while len(stack) > 1:
                        os.close(stack.pop())
                pending.extendleft(reversed(target.parts[1:] if target.is_absolute() else target.parts))
                continue
            is_directory = bool(pending) or directory
            flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC | os.O_NONBLOCK
            fd = os.open(name, flags | (os.O_DIRECTORY if is_directory else 0), dir_fd=stack[-1])
            info = os.fstat(fd)
            valid = stat.S_ISDIR(info.st_mode) if is_directory else stat.S_ISREG(info.st_mode) and info.st_size <= 256 * 1024 * 1024
            if not valid or info.st_uid != 0 or info.st_mode & 0o022:
                os.close(fd)
                raise MaintenanceError('maintenance_input_invalid')
            if not pending:
                return fd
            stack.append(fd)
        if directory:
            return os.dup(stack[-1])
        raise MaintenanceError('maintenance_input_invalid')
    finally:
        for fd in stack:
            os.close(fd)

def file_digest(path):
    fd = open_input(path)
    try:
        h = hashlib.sha256()
        with os.fdopen(fd, 'rb', closefd=False) as source:
            while chunk := source.read(1024 * 1024):
                h.update(chunk)
        return h.hexdigest()
    finally:
        os.close(fd)

def tree_digest(paths, empty_scratch=(), scratch_aliases=None):
    files = []
    for base in list(paths):
        if not base:
            continue  # Unset optional APT paths never mean the current directory.
        path = Path(base)
        if path.is_file():
            files.append(path)
        elif path.is_dir():
            os.close(open_input(path, directory=True))
            for child in path.rglob('*'):
                if any(scratch in child.parents for scratch in empty_scratch):
                    continue  # The declared scratch directory was validated as a unit.
                if child.name == 'lock' or 'partial' in child.parts:
                    continue
                if child in empty_scratch:
                    # libapt's local mirror cache contains root-owned aliases to
                    # declared protected lists. Hash the original inputs, not this
                    # _apt-writable directory; no other auxiliary data is accepted.
                    if child.is_symlink() or not child.is_dir():
                        raise MaintenanceError('maintenance_input_invalid')
                    for alias in child.iterdir():
                        target = (scratch_aliases or {}).get(alias)
                        info = alias.lstat()
                        if (target is None or not stat.S_ISLNK(info.st_mode) or
                                info.st_uid != 0 or os.readlink(alias) != str(target)):
                            raise MaintenanceError('maintenance_input_invalid')
                        file_digest(target)
                elif child.is_dir():
                    os.close(open_input(child, directory=True))
                elif child.is_file():
                    files.append(child)
        else:
            # Even an absent optional leaf needs a trusted parent: it must not be
            # inserted by another user between the final hash and native use.
            os.close(open_input(path.parent, directory=True))
    return digest([(str(p), file_digest(p)) for p in sorted(set(files))])

def configure(upgrade=True):
    # Never honor inherited caller-controlled config, interpreter paths or locale.
    for key in ['APT_CONFIG', 'PYTHONPATH', 'PYTHONHOME', 'LD_PRELOAD', 'LD_LIBRARY_PATH']:
        if key in os.environ:
            raise MaintenanceError('maintenance_environment_invalid')
    os.environ.update(DEBIAN_FRONTEND='noninteractive', NEEDRESTART_MODE='l', UCF_FORCE_CONFFOLD='1',
                      LC_ALL='C.UTF-8', TZ='UTC', APT_LISTCHANGES_FRONTEND='none')
    supported_hooks(refresh_only=not upgrade)
    for key in ['APT::Get::AllowUnauthenticated', 'Acquire::AllowInsecureRepositories',
                'Acquire::AllowWeakRepositories', 'Acquire::AllowDowngradeToInsecureRepositories']:
        if apt_pkg.config.find_b(key, False):
            raise MaintenanceError('maintenance_repository_policy')
    for key in ['Acquire::Check-Valid-Until', 'Acquire::https::Verify-Peer', 'Acquire::https::Verify-Host']:
        if not apt_pkg.config.find_b(key, True):
            raise MaintenanceError('maintenance_repository_policy')
    for key in apt_pkg.config.keys():
        lower = key.lower()
        if lower.endswith(('::verify-peer', '::verify-host', '::check-valid-until', '::check-date')) and not apt_pkg.config.find_b(key, True):
            raise MaintenanceError('maintenance_repository_policy')
        if (upgrade and lower.startswith('acquire::') and apt_pkg.config.find(key) and
                any(part in lower.split('::') for part in ('min-validtime', 'max-validtime', 'max-futuretime'))):
            raise MaintenanceError('maintenance_repository_policy')
    if upgrade:
        if any(value not in ['--force-confdef', '--force-confold'] for value in apt_pkg.config.value_list('DPkg::Options')):
            raise MaintenanceError('maintenance_package_policy')
        if (apt_pkg.config.find_file('Dir::Bin::dpkg') != '/usr/bin/dpkg' or
                apt_pkg.config.find_dir('Dir::Bin::methods').rstrip('/') != '/usr/lib/apt/methods'):
            raise MaintenanceError('maintenance_package_policy')
        apt_pkg.config.clear('DPkg::Options')
        apt_pkg.config.set('DPkg::Options::', '--force-confdef')
        apt_pkg.config.set('DPkg::Options::', '--force-confold')
        if apt_pkg.config.find('APT::Sandbox::User', '_apt') != '_apt':
            raise MaintenanceError('maintenance_download_sandbox')
        import pwd
        partial = Path(apt_pkg.config.find_dir('Dir::Cache::archives')) / 'partial'
        if not partial.is_dir() or partial.stat().st_uid != pwd.getpwnam('_apt').pw_uid:
            raise MaintenanceError('maintenance_download_sandbox')

def budget_clock():
    return time.clock_gettime(time.CLOCK_BOOTTIME)

@contextlib.contextmanager
def readonly_budget(child, seconds=30):
    # Only fixed metadata readers use this watchdog, never PackageManager/dpkg.
    deadline = budget_clock() + seconds
    finished, expired = threading.Event(), threading.Event()
    def stop():
        if child.poll() is None:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    def watch():
        while not finished.wait(0.1):
            if budget_clock() >= deadline:
                expired.set()
                stop()
                return
    watcher = threading.Thread(target=watch, daemon=True)
    watcher.start()
    try:
        yield
        if budget_clock() >= deadline or expired.is_set():
            raise MaintenanceError('maintenance_reader_timeout')
    finally:
        finished.set()
        watcher.join()
        stop()
        child.wait()

def readonly_output(command, limit=65536, seconds=30):
    child = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                             stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        with readonly_budget(child, seconds):
            output = child.stdout.read(limit + 1)
            if len(output) > limit or child.wait() != 0:
                raise MaintenanceError('maintenance_reader_failed')
            return output
    finally:
        child.stdout.close()

@contextlib.contextmanager
def package_lock():
    deadline = budget_clock() + 30
    while True:
        if budget_clock() >= deadline:
            raise MaintenanceError('maintenance_busy')
        lock = apt_pkg.SystemLock()
        try:
            lock.__enter__()
            break
        except apt_pkg.Error:
            if budget_clock() >= deadline:
                raise MaintenanceError('maintenance_busy') from None
            time.sleep(0.1)
    try:
        with apt_pkg.FileLock(apt_pkg.config.find_dir('Dir::State::lists') + 'lock'):
            yield
    finally:
        lock.__exit__(None, None, None)

def audit():
    child = subprocess.Popen(['/usr/bin/dpkg', '--audit'], stdin=subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        with readonly_budget(child):
            if child.stdout.read(1) or child.wait() != 0:
                raise MaintenanceError('maintenance_package_state')
    finally:
        child.stdout.close()

class FetchProgress(apt.progress.base.AcquireProgress):
    def __init__(self):
        self.started = self.changed = budget_clock()
        self.previous = 0
        self.failed = False

    def fail(self, item):
        self.failed = True

    def media_change(self, media, drive):
        return False

    def pulse(self, owner):
        now = budget_clock()
        if self.current_bytes != self.previous:
            self.changed, self.previous = now, self.current_bytes
        return now - self.started < 1800 and now - self.changed < 120

    def within_budget(self):
        now = budget_clock()
        return now - self.started < 1800 and now - self.changed < 120

def source_security(name, value, upgrade=True):
    name, value = name.lower(), value.lower()
    truth, false = ('yes', 'true', '1', 'on'), ('no', 'false', '0', 'off')
    if (name in ('trusted', 'allow-insecure', 'allow-weak', 'allow-downgrade-to-insecure') and value in truth or
            name in ('check-valid-until', 'check-date') and value in false):
        raise MaintenanceError('maintenance_repository_policy')
    if upgrade and name in ('valid-until-min', 'valid-until-max', 'date-max-future', 'inrelease-path'):
        raise MaintenanceError('maintenance_repository_policy')


def trusted_sources(upgrade=True):
    paths = [apt_pkg.config.find_file('Dir::Etc::sourcelist'), apt_pkg.config.find_dir('Dir::Etc::sourceparts')]
    for base in list(paths):
        p = Path(base)
        files = list(p.glob('*.sources')) + list(p.glob('*.list')) if p.is_dir() else [p]
        for source in files:
            if not source.is_file():
                continue
            file_digest(source)
            if source.suffix == '.sources':
                with source.open() as content:
                    for section in apt_pkg.TagFile(content):
                        if section.get('Enabled', 'yes').lower() in ('no', 'false', '0', 'off'):
                            continue
                        for key in section.keys():
                            source_security(key, section[key].strip(), upgrade)
                            if key.lower() == 'signed-by':
                                paths.extend(token for token in section[key].split() if token.startswith('/'))
            else:
                for line in source.read_text().splitlines():
                    if not line.strip() or line.lstrip().startswith('#'):
                        continue
                    match = re.match(r'^\s*deb(?:-src)?\s+\[([^]]*)\]', line)
                    if match:
                        for option in match[1].split():
                            pair = option.split('=', 1)
                            if len(pair) != 2 or any(c in option for c in '\"\''):
                                raise MaintenanceError('maintenance_repository_policy')
                            source_security(*pair, upgrade=upgrade)
                            if pair[0].lower() == 'signed-by':
                                paths.extend(token for token in pair[1].split(',') if token.startswith('/'))
    return paths
