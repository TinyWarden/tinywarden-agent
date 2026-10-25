"""Bind every native PackageManager input to inspected, approved cache bytes."""
import tarfile
import posixpath
from urllib.parse import urlsplit, unquote

POLICY.update(mirror_policy=1)


@contextlib.contextmanager
def protected_mirror_cache():
    # Hold native locks before entering. _apt may normally replace auxiliary
    # entries; it must not redirect a mirror list while this root worker uses it.
    import pwd
    parent = open_input(apt_pkg.config.find_dir('Dir::State::lists'), directory=True)
    fd = None
    original = None
    try:
        try:
            os.mkdir('auxfiles', 0o755, dir_fd=parent)
        except FileExistsError:
            pass
        fd = os.open('auxfiles', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent)
        original = os.fstat(fd)
        if original.st_uid not in (0, pwd.getpwnam('_apt').pw_uid) or original.st_mode & 0o022:
            original = None
            raise MaintenanceError('maintenance_input_invalid')
        os.fchown(fd, 0, -1)  # Preserve the directory's existing group access.
        yield
    finally:
        if fd is not None:
            try:
                if original is not None:
                    os.fchown(fd, original.st_uid, original.st_gid)
                    os.fchmod(fd, stat.S_IMODE(original.st_mode))
            finally:
                os.close(fd)
        os.close(parent)


def mirror_sources(cache):
    # The source list, not a package URI's guessed path, identifies the mirror file.
    mirrors = {}
    for source in cache._list.list:
        uri = source.uri.rstrip('/')
        parsed = urlsplit(uri)
        if parsed.scheme != 'mirror+file':
            continue
        if parsed.netloc or parsed.query or parsed.fragment:
            raise MaintenanceError('maintenance_archive_method')
        path = Path(unquote(parsed.path, errors='strict'))
        with os.fdopen(open_input(path), 'rb') as content:
            raw = content.read(1024 * 1024 + 1)
        if len(raw) > 1024 * 1024:
            raise MaintenanceError('maintenance_archive_method')
        try:
            lines = raw.decode('utf-8').splitlines()
        except UnicodeError:
            raise MaintenanceError('maintenance_archive_method') from None
        count = 0
        for line in lines:
            if not line or line.startswith('#'):
                continue
            # APT separates selection metadata with TAB. Every possible mirror,
            # including fallbacks and index-only entries, must use HTTP(S).
            address = line.split('\t', 1)[0]
            target = urlsplit(address)
            if (target.scheme not in POLICY['archive_methods'] or not target.hostname or
                    target.fragment or any(c.isspace() or ord(c) < 32 for c in address)):
                raise MaintenanceError('maintenance_archive_method')
            count += 1
        if not count:
            raise MaintenanceError('maintenance_archive_method')
        mirrors[uri] = path
    return mirrors


def mirror_aliases(mirrors):
    directory = Path(apt_pkg.config.find_dir('Dir::State::lists')) / 'auxfiles'
    return {directory / apt_pkg.uri_to_filename(uri.removeprefix('mirror+')): path
            for uri, path in mirrors.items()}


def archive_items(fetcher, mirrors):
    aliases = mirror_aliases(mirrors)
    auxiliary = {uri.removeprefix('mirror+'): path for uri, path in mirrors.items()}
    archives = []
    for item in fetcher.items:
        original = auxiliary.get(item.desc_uri)
        if original is None:
            archives.append(item)
        else:
            # libapt adds an auxiliary item after starting a mirror acquisition.
            # Its local file is the declared, protected mirror LIST, never a deb.
            destinations = [original, *[a for a, p in aliases.items() if p == original]]
            if not item.complete or Path(item.destfile) not in destinations:
                raise MaintenanceError('maintenance_archive_invalid')
            file_digest(original)
    return archives


def archive_methods(uris, mirrors=None):
    if not uris:
        raise MaintenanceError('maintenance_archive_method')
    for uri in uris:
        if urlsplit(uri).scheme in POLICY['archive_methods']:
            continue
        if not any(uri.startswith(base + '/') for base in (mirrors or {})):
            raise MaintenanceError('maintenance_archive_method')


def cache_path(raw, downloading=False):
    directory = Path(apt_pkg.config.find_dir('Dir::Cache::archives')).absolute()
    path = Path(raw).absolute()
    allowed = (directory, directory / 'partial') if downloading else (directory,)
    if path.parent not in allowed or path.suffix != '.deb':
        raise MaintenanceError('maintenance_archive_invalid')
    # Final files and ancestors cannot be replaced by an unprivileged process.
    for parent in [directory, *directory.parents]:
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise MaintenanceError('maintenance_archive_invalid')
    if path.is_symlink():
        raise MaintenanceError('maintenance_archive_invalid')
    return path


@contextlib.contextmanager
def deb_archive(path, control=False):
    flag = '--ctrl-tarfile' if control else '--fsys-tarfile'
    child = subprocess.Popen(['/usr/bin/dpkg-deb', flag, str(path)],
                             stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                             stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        with readonly_budget(child):
            with tarfile.open(fileobj=child.stdout, mode='r|') as content:
                yield content
            if child.wait() != 0:
                raise MaintenanceError('maintenance_archive_invalid')
    except (tarfile.TarError, OSError, EOFError):
        raise MaintenanceError('maintenance_archive_invalid') from None
    finally:
        child.stdout.close()


def archive_metadata(path, expected):
    paths, payload, declarations, control = [], [], [], None
    for is_control in (False, True):
        seen = set()
        with deb_archive(path, is_control) as content:
            for index, member in enumerate(content):
                name = canonical_path(member.name)
                if index >= 200000 or name in seen or not (member.isfile() or member.isdir() or member.issym() or member.islnk()):
                    raise MaintenanceError('maintenance_metadata_invalid')
                seen.add(name)
                if not is_control:
                    paths.append(name)
                    kind = ('directory' if member.isdir() else 'symlink' if member.issym()
                            else 'hardlink' if member.islnk() else 'file')
                    payload.append({'path': name, 'kind': kind, 'bytes': member.size})
                    if member.issym() or member.islnk():
                        target = member.linkname
                        if not target.startswith('/'):
                            target = str((PurePosixPath('/') if member.islnk() else PurePosixPath(name).parent) / target)
                        paths.append(canonical_path(posixpath.normpath(target)))
                elif name in ('/control', '/triggers'):
                    if not member.isfile() or member.size > 1024 * 1024:
                        raise MaintenanceError('maintenance_metadata_invalid')
                    data = content.extractfile(member).read().decode('utf-8', errors='strict')
                    if name == '/triggers':
                        declarations = triggers(data)
                    else:
                        control = apt_pkg.TagSection(data)
    if control is None or any(control.get(field) != expected[key] for field, key in
            [('Package', 'name'), ('Version', 'candidate_version'), ('Architecture', 'architecture')]):
        raise MaintenanceError('maintenance_archive_invalid')
    return {'name': expected['name'], 'section': control.get('Section', ''),
            'paths': paths, 'triggers': declarations, 'payload': payload}


def verify_inputs(fetcher, plan, installed, mirrors=None):
    mirrors = mirrors or {}
    items = archive_items(fetcher, mirrors)
    wanted = {p['archive_sha256']: p for p in plan['packages']}
    if len(wanted) != len(plan['packages']) or len(items) != len(wanted):
        raise MaintenanceError('maintenance_archive_invalid')
    changes = {}
    for item in items:
        path = cache_path(item.destfile)
        info = path.lstat()
        if (not item.is_trusted or not item.complete or not stat.S_ISREG(info.st_mode) or
                info.st_uid != 0 or info.st_mode & 0o022):
            raise MaintenanceError('maintenance_archive_invalid')
        if item.desc_uri:
            archive_methods([item.desc_uri], mirrors)
        sha = file_digest(path)
        record = wanted.pop(sha, None)
        if record is None or info.st_size != record['archive_bytes']:
            raise MaintenanceError('maintenance_archive_invalid')
        changes[(record['name'], record['architecture'])] = archive_metadata(path, record)
    if wanted:
        raise MaintenanceError('maintenance_archive_invalid')
    boot_effects(changes, installed)
    capacity(plan, changes=changes)


def acquisition(cache, plan):
    mirrors = mirror_sources(cache)
    pm = apt_pkg.PackageManager(cache._depcache)
    progress = FetchProgress()
    fetcher = apt_pkg.Acquire(progress)
    if not pm.get_archives(fetcher, cache._list, apt_pkg.PackageRecords(cache._cache)):
        raise MaintenanceError('maintenance_archive_invalid')
    # Do not accept a missing native input merely because another cached file has its hash.
    if len(fetcher.items) != len(plan['packages']):
        raise MaintenanceError('maintenance_archive_invalid')
    for item in fetcher.items:
        if not item.is_trusted:
            raise MaintenanceError('maintenance_repository_policy')
        cache_path(item.destfile, downloading=True)
        if item.desc_uri:
            archive_methods([item.desc_uri], mirrors)
        elif not item.complete:
            raise MaintenanceError('maintenance_archive_method')
    return pm, fetcher, progress
