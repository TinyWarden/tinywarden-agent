"""One native transaction per approved digest, with no implicit repair/refetch loop."""
def approved_plan(expected):
    cache, plan = resolve()
    if digest(plan) != expected:
        raise MaintenanceError('maintenance_plan_changed')
    capacity(plan, installed=installed_metadata(cache))
    return cache, plan


def fetch(expected):
    audit()
    with package_lock(), protected_mirror_cache():
        cache, plan = approved_plan(expected)
        with apt_pkg.FileLock(apt_pkg.config.find_dir('Dir::Cache::archives') + 'lock'):
            _, fetcher, progress = acquisition(cache, plan)
            if fetcher.run() != apt_pkg.Acquire.RESULT_CONTINUE or progress.failed or not progress.within_budget():
                raise MaintenanceError('maintenance_download_failed')
            if apt_pkg.config.find('APT::Sandbox::User', '_apt') != '_apt':
                raise MaintenanceError('maintenance_download_sandbox')
            verify_inputs(fetcher, plan, installed_metadata(cache), mirror_sources(cache))
        return {'sha256': digest(plan), 'downloaded': True}


def apply(expected, start):
    global EFFECT
    # The coordinator must durably consume its current start grant before invocation.
    audit()
    with package_lock(), protected_mirror_cache():
        cache, plan = approved_plan(expected)
        if not plan['packages']:
            return {'outcome': 'no_changes', 'effect': 'none'}
        with apt_pkg.FileLock(apt_pkg.config.find_dir('Dir::Cache::archives') + 'lock'):
            before = {p.shortname + ':' + p.installed.architecture: p.installed.version for p in cache if p.installed}
            expected_installed = dict(before)
            for p in plan['packages']:
                expected_installed[p['name'] + ':' + p['architecture']] = p['candidate_version']
            pm, fetcher, _ = acquisition(cache, plan)
            if fetcher.fetch_needed:
                raise MaintenanceError('maintenance_download_required')
            verify_inputs(fetcher, plan, installed_metadata(cache), mirror_sources(cache))
            start_allowed(start)
            # SAME PackageManager. No acquire.run, repair, refetch or second native attempt.
            EFFECT = 'unknown'
            result = cache.install_archives(pm, apt.progress.base.InstallProgress())
            if result != pm.RESULT_COMPLETED:
                raise MaintenanceError('maintenance_apply_incomplete')
        audit()
        fresh = apt.Cache(progress=apt.progress.base.OpProgress())
        if fresh.broken_count:
            raise MaintenanceError('maintenance_package_state')
        actual = {p.shortname + ':' + p.installed.architecture: p.installed.version for p in fresh if p.installed}
        if actual != expected_installed:
            raise MaintenanceError('maintenance_verification_failed')
        if selections(fresh) != plan['selection_sha256']:
            raise MaintenanceError('maintenance_selection_changed')
        EFFECT = 'packages_changed'
        return {'outcome': 'packages_applied', 'effect': 'packages_changed',
                'state_sha256': file_digest(apt_pkg.config.find_file('Dir::State::status')),
                'selection_sha256': selections(fresh),
                'reboot_required': Path('/run/reboot-required').exists(),
                'configuration_review': configuration_review(fresh, plan),
                'services_restart_required': service_review()}

def configuration_review(cache, plan):
    # Report retained replacement files for changed packages, not raw configuration.
    # UCF registrations cover equivalent .ucf-dist files outside dpkg conffiles.
    try:
        changed = {(p['name'], p['architecture']) for p in plan['packages']}
        paths = set()
        for package in cache:
            if package.installed and (package.shortname, package.installed.architecture) in changed:
                for line in package.installed.record.get('Conffiles', '').splitlines():
                    fields = line.split()
                    if fields:
                        paths.add(canonical_path(fields[0]))
        registry = Path('/var/lib/ucf/registry')
        if registry.exists():
            file_digest(registry)
            for line in registry.read_text().splitlines():
                fields = line.split()
                if len(fields) != 2:
                    return None
                paths.add(canonical_path(fields[1]))
        return any(Path(path + suffix).exists() or Path(path + suffix).is_symlink()
                   for path in paths for suffix in ('.dpkg-dist', '.dpkg-merge', '.ucf-dist'))
    except (OSError, ValueError, MaintenanceError):
        return None

def service_review():
    # Debian batch/list mode reports outstanding service work, never restarts it.
    if not Path('/usr/sbin/needrestart').is_file():
        return None
    try:
        file_digest('/usr/sbin/needrestart')
        output = readonly_output(['/usr/sbin/needrestart', '-b', '-r', 'l', '-l']).decode('utf-8')
        if not any(line.startswith('NEEDRESTART-VER: ') for line in output.splitlines()):
            return None
        return any(line.startswith('NEEDRESTART-SVC: ') for line in output.splitlines())
    except (OSError, UnicodeError, MaintenanceError):
        return None

def verify():
    proof = globals().get('MAINTENANCE_VERIFY')
    if (not isinstance(proof, dict) or set(proof) != {'state_sha256', 'selection_sha256'} or
            any(not isinstance(v, str) or not re.fullmatch('[0-9a-f]{64}', v) for v in proof.values())):
        raise MaintenanceError('maintenance_receipt_invalid')
    with package_lock():
        audit()
        fresh = apt.Cache(progress=apt.progress.base.OpProgress())
        if (fresh.broken_count or selections(fresh) != proof['selection_sha256'] or
                file_digest(apt_pkg.config.find_file('Dir::State::status')) != proof['state_sha256']):
            raise MaintenanceError('maintenance_verification_failed')
    return {'verified': True}

def inspect_locked():
    # Fixed read-only inspection. No configure/refresh/resolve/acquire/commit hooks.
    if os.geteuid() != 0:
        raise MaintenanceError('maintenance_root_required')
    if any(k in os.environ for k in ('APT_CONFIG', 'PYTHONPATH', 'PYTHONHOME', 'LD_PRELOAD', 'LD_LIBRARY_PATH')):
        raise MaintenanceError('maintenance_environment_invalid')
    with package_lock():
        audit()
        fresh = apt.Cache(progress=apt.progress.base.OpProgress())
        if fresh.broken_count:
            raise MaintenanceError('maintenance_package_state')
        value = {'schema_version': 1, 'facts': {
            'state_sha256': file_digest(apt_pkg.config.find_file('Dir::State::status')),
            'selection_sha256': selections(fresh)}}
        raw = canonical(value) + b'\n'
        while raw:
            raw = raw[os.write(RESULT_FD, raw):]
        # Parent performs a local fsynced journal/fence commit while locks stay held.
        # EOF/death simply ends this read-only helper; it cannot continue old work.
        os.read(0, 1)
