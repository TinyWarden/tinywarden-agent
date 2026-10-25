"""Structured, exact conservative plans; no simulation-output authorization."""
POLICY.update(validity_policy=1)

def valid_release_dates(section, now):
    try:
        created = apt_pkg.str_to_time(section['Date'])
        expires = apt_pkg.str_to_time(section['Valid-Until']) if 'Valid-Until' in section else None
    except (KeyError, ValueError, OverflowError):
        raise MaintenanceError('maintenance_repository_expired') from None
    if type(created) is not int or 'Valid-Until' in section and type(expires) is not int:
        raise MaintenanceError('maintenance_repository_expired')
    if created <= 0 or created > now + 10 or expires is not None and (expires <= created or expires <= now):
        raise MaintenanceError('maintenance_repository_expired')

def valid_indexes(cache):
    # Cache trust does not itself recheck a cached Release's wall-clock expiry.
    # Ask libapt for its actual index destinations without running an acquisition.
    fetcher = apt_pkg.Acquire()
    try:
        if not cache._list.get_indexes(fetcher, True) or any(not m.is_trusted for m in cache._list.list):
            raise MaintenanceError('maintenance_repository_policy')
        directory = Path(apt_pkg.config.find_dir('Dir::State::lists'))
        indexes = [item for item in fetcher.items if item.desc_uri.endswith('/InRelease')]
        if len(indexes) != len(cache._list.list):
            raise MaintenanceError('maintenance_repository_policy')
        for item in indexes:
            if Path(item.destfile).parent != directory / 'partial':
                raise MaintenanceError('maintenance_repository_policy')
            path = directory / Path(item.destfile).name
            if not path.exists():
                path = path.with_name(path.name.removesuffix('InRelease') + 'Release')
            file_digest(path)
            with os.fdopen(apt_pkg.open_maybe_clear_signed_file(str(path))) as source:
                records = apt_pkg.TagFile(source)
                section = next(records, None)
                if section is None or next(records, None) is not None:
                    raise MaintenanceError('maintenance_repository_expired')
                valid_release_dates(section, time.time())
    finally:
        fetcher.shutdown()

def resolve():
    cache = apt.Cache(progress=apt.progress.base.OpProgress())
    if cache.broken_count:
        raise MaintenanceError('maintenance_package_state')
    mirrors = mirror_sources(cache)
    valid_indexes(cache)
    cache.upgrade(dist_upgrade=False)
    packages = []
    installed = installed_metadata(cache)
    changes = {}
    for package in cache.get_changes():
        old, new = package.installed, package.candidate
        if (not old or not new or package.marked_delete or package.marked_reinstall or
                package._pkg.selected_state == apt_pkg.SELSTATE_HOLD or
                new.architecture != old.architecture or apt_pkg.version_compare(new.version, old.version) <= 0):
            raise MaintenanceError('maintenance_package_policy')
        if not new.downloadable or not new.origins or not all(origin.trusted for origin in new.origins):
            raise MaintenanceError('maintenance_repository_policy')
        if not re.fullmatch('[0-9a-f]{64}', new.sha256):
            raise MaintenanceError('maintenance_archive_invalid')
        archive_methods(new.uris, mirrors)
        changes[(package.shortname, old.architecture)] = dict(installed[(package.shortname, old.architecture)], section=new.section)
        packages.append({'name': package.shortname, 'architecture': new.architecture,
                         'installed_version': old.version, 'candidate_version': new.version,
                         'archive_sha256': new.sha256, 'archive_bytes': new.size,
                         'installed_bytes': new.installed_size})
    boot_effects(changes, installed)
    packages.sort(key=lambda p: (p['name'], p['architecture']))
    if len(packages) > MAX_ROWS:
        raise MaintenanceError('maintenance_plan_limit')
    changed = {(p['name'], p['architecture']) for p in packages}
    kept = sorted([{'name': p.shortname, 'architecture': p.installed.architecture,
                    'held': p._pkg.selected_state == apt_pkg.SELSTATE_HOLD}
                   for p in cache if p.installed and p.is_upgradable and
                   (p.shortname, p.installed.architecture) not in changed], key=lambda p: (p['name'], p['architecture']))
    if len(kept) > MAX_ROWS:
        raise MaintenanceError('maintenance_plan_limit')
    sources = [*trusted_sources(), *mirrors.values()]
    plan = {'schema_version': 1, 'action': 'packages.upgrade_conservative.v1', 'provider_version': 1,
            'policy_sha256': digest(POLICY),
            'state_sha256': file_digest(apt_pkg.config.find_file('Dir::State::status')),
            'sources_sha256': tree_digest(sources),
            'indexes_sha256': tree_digest([apt_pkg.config.find_dir('Dir::State::lists')],
                                         [Path(apt_pkg.config.find_dir('Dir::State::lists')) / 'auxfiles'],
                                         mirror_aliases(mirrors)),
            'config_sha256': digest(apt_pkg.config.dump()), 'local_sha256': local_identity(),
            'selection_sha256': selections(cache),
            'packages': packages, 'kept_back': kept,
            'download_bytes': sum(p['archive_bytes'] for p in packages),
            'unpacked_bytes': sum(p['installed_bytes'] for p in packages)}
    if len(canonical(plan)) > MAX_PLAN_BYTES:
        raise MaintenanceError('maintenance_plan_limit')
    return cache, plan

def selections(cache):
    return digest(sorted((p.shortname, p.installed.architecture, p._pkg.selected_state,
                          p.is_auto_installed) for p in cache if p.installed))

def capacity_directory(raw):
    # Resolve existing parent aliases (for example merged /usr), never a payload
    # symlink's target. Missing directories use their nearest actual ancestor.
    try:
        path = Path(raw).resolve(strict=False)
        while not path.exists():
            path = path.parent
        for ancestor in [path, *path.parents]:
            info = ancestor.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
                raise MaintenanceError('maintenance_destination_invalid')
        return path
    except (OSError, RuntimeError):
        raise MaintenanceError('maintenance_destination_invalid') from None

def capacity(plan, installed=None, changes=None):
    # A footprint estimate with reserve, not a bound on arbitrary script writes.
    paths = {capacity_directory('/'),
             capacity_directory(Path(apt_pkg.config.find_file('Dir::State::status')).parent),
             capacity_directory(apt_pkg.config.find_dir('Dir::Cache::archives'))}
    payload_bytes, payload_inodes = 0, 0
    if installed is not None:
        for package in plan['packages']:
            key = (package['name'], package['architecture'])
            for name in installed[key]['paths']:
                path = Path(name)
                try:
                    if stat.S_ISDIR(path.lstat().st_mode):
                        continue
                except FileNotFoundError:
                    pass
                paths.add(capacity_directory(path.parent))
    if changes is not None:
        payload = [entry for value in changes.values() for entry in value['payload']]
        aliases = {entry['path'] for entry in payload if entry['kind'] in ('symlink', 'hardlink')}
        for entry in payload:
            path = Path(entry['path'])
            if any(str(parent) in aliases for parent in path.parents):
                raise MaintenanceError('maintenance_destination_invalid')
            if entry['kind'] == 'directory' and path.is_dir():
                continue
            paths.add(capacity_directory(path.parent))
            payload_bytes += max(entry['bytes'], 4096)
            payload_inodes += 1
    required = (plan['download_bytes'] + POLICY['unpack_multiplier'] *
                max(plan['unpacked_bytes'], payload_bytes) + POLICY['reserve_bytes'])
    inodes = POLICY['reserve_inodes'] + max(payload_inodes, len(plan['packages']) * POLICY['inodes_per_package'])
    # Check every relevant path's flags, even bind mounts sharing a device number.
    # Existing empty directories and terminal symlink targets never invent writes
    # into an unrelated /proc, EFI or other small filesystem.
    for path in paths:
        fs = os.statvfs(path)
        if (not os.access(path, os.W_OK) or fs.f_flag & os.ST_RDONLY or
                fs.f_bavail * fs.f_frsize < required or fs.f_favail < inodes):
            raise MaintenanceError('maintenance_disk_space')

def prepare():
    audit()
    with package_lock(), protected_mirror_cache():
        cache, plan = resolve()
        capacity(plan, installed=installed_metadata(cache))
        return {'plan': plan, 'sha256': digest(plan)}
