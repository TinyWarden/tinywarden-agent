"""Versioned Debian boot-effect policy, including declared trigger effects."""
from fnmatch import fnmatchcase
from pathlib import PurePosixPath

BOOT_PACKAGES = ('linux-image*', 'initramfs-tools*', 'dracut*', 'grub*', 'shim*',
                 'systemd-boot*', 'systemd-ukify', 'flash-kernel', 'u-boot*')
BOOT_PATHS = ('/boot', '/efi', '/lib/modules', '/usr/lib/modules', '/etc/kernel',
              '/usr/lib/kernel', '/etc/initramfs-tools', '/usr/share/initramfs-tools',
              '/usr/lib/initramfs-tools', '/etc/dracut.conf', '/etc/dracut.conf.d',
              '/usr/lib/dracut', '/usr/share/dracut', '/etc/grub.d', '/etc/default/grub',
              '/usr/lib/grub', '/usr/share/grub', '/usr/lib/shim', '/usr/share/shim')
POLICY.update(boot_policy=1, boot_packages=BOOT_PACKAGES, boot_paths=BOOT_PATHS,
              archive_methods=('http', 'https'), reserve_bytes=512 * 1024 * 1024,
              reserve_inodes=1024, unpack_multiplier=2, inodes_per_package=64,
              capacity_policy=2)

def canonical_path(value):
    if not isinstance(value, str) or len(value) > 4096 or '\x00' in value or '\n' in value:
        raise MaintenanceError('maintenance_metadata_invalid')
    parts = PurePosixPath(value).parts
    if '..' in parts:
        raise MaintenanceError('maintenance_metadata_invalid')
    return '/' + '/'.join(p for p in parts if p not in ('/', '.'))

def under(path, prefix):
    return path == prefix or path.startswith(prefix + '/')

def boot_subject(name, section, paths):
    return (section.rsplit('/', 1)[-1] == 'kernel' or
            any(fnmatchcase(name, pattern) for pattern in BOOT_PACKAGES) or
            any(under(path, prefix) for path in paths for prefix in BOOT_PATHS))

def triggers(content):
    result = []
    for line in content.splitlines():
        fields = line.split('#', 1)[0].split()
        if not fields:
            continue
        if (len(fields) != 2 or fields[0] not in ('interest', 'interest-await',
                'interest-noawait', 'activate', 'activate-await', 'activate-noawait') or
                len(fields[1]) > 4096):
            raise MaintenanceError('maintenance_metadata_invalid')
        result.append((fields[0].split('-')[0], fields[1]))
    return result

def installed_metadata(cache):
    info = Path(apt_pkg.config.find_file('Dir::State::status')).parent / 'info'
    result = {}
    for package in cache:
        if not package.installed:
            continue
        name, arch = package.shortname, package.installed.architecture
        stems = [info / name, info / (name + ':' + arch)]
        stems = [p for p in stems if p.with_name(p.name + '.list').exists()]
        if len(stems) != 1:
            raise MaintenanceError('maintenance_metadata_invalid')
        listing = stems[0].with_name(stems[0].name + '.list')
        file_digest(listing)
        paths = [canonical_path(line) for line in listing.read_text().splitlines()]
        triggerfile = stems[0].with_name(stems[0].name + '.triggers')
        declarations = []
        if triggerfile.exists() or triggerfile.is_symlink():
            file_digest(triggerfile)
            declarations = triggers(triggerfile.read_text())
        result[(name, arch)] = {'name': name, 'section': package.installed.section,
                               'paths': paths, 'triggers': declarations}
    return result

def boot_effects(changes, installed):
    # A trigger may execute another installed package's postinst without upgrading it.
    handlers = {}
    for key, record in installed.items():
        for mode, trigger in record['triggers']:
            if mode == 'interest':
                handlers.setdefault(trigger, set()).add(key)
    impacted, reasons = set(changes), set()
    pending = list(changes)
    while pending:
        key = pending.pop()
        records = [r for r in (installed.get(key), changes.get(key)) if r is not None]
        if not records:
            raise MaintenanceError('maintenance_metadata_invalid')
        if any(boot_subject(r['name'], r['section'], r['paths']) for r in records):
            reasons.add(key)
        activated = {t for r in records for mode, t in r['triggers'] if mode == 'activate'}
        paths = {p for r in records for p in r['paths']}
        activated.update(t for t in handlers if t.startswith('/') and
                         any(under(p, canonical_path(t)) for p in paths))
        for trigger in activated:
            for handler in handlers.get(trigger, ()):
                if handler not in impacted:
                    impacted.add(handler)
                    pending.append(handler)
    if reasons:
        # Keep the entire requested transaction blocked and name both changed and handler subjects.
        subjects = sorted({name + ':' + arch for name, arch in reasons | set(changes)})
        raise MaintenanceError('maintenance_boot_review_required', subjects)

def local_identity():
    database = Path(apt_pkg.config.find_file('Dir::State::status')).parent
    paths = [database / 'info', database / 'triggers', '/etc/kernel', '/etc/initramfs-tools',
             '/etc/dracut.conf', '/etc/dracut.conf.d', '/etc/grub.d', '/etc/default/grub',
             '/etc/apt/apt.conf.d', '/etc/apt/apt.conf', '/etc/needrestart', *HOOK_FILES,
             apt_pkg.config.find_file('Dir::Etc::preferences'), apt_pkg.config.find_dir('Dir::Etc::preferencesparts'),
             apt_pkg.config.find_file('Dir::Etc::trusted'), apt_pkg.config.find_dir('Dir::Etc::trustedparts')]
    return tree_digest(paths)
