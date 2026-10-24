"""Supported Debian noninteractive hooks are fixed provider policy, never app inputs."""
HOOKS = {'DPkg::Pre-Install-Pkgs': (
    '/usr/bin/apt-listchanges --apt || test $? -lt 10',
    '/usr/sbin/dpkg-preconfigure --apt || true'),
    'DPkg::Post-Invoke': ('/usr/lib/needrestart/apt-pinvoke',
        'test -x /usr/lib/needrestart/apt-pinvoke && /usr/lib/needrestart/apt-pinvoke || true')}
HOOK_FILES = ('/usr/bin/apt-listchanges', '/usr/sbin/dpkg-preconfigure',
              '/usr/lib/needrestart/apt-pinvoke', '/usr/sbin/needrestart')
POLICY.update(hooks=HOOKS, debconf='noninteractive-defaults', ucf='keep-local',
              apt_listchanges='none')

def supported_hooks(refresh_only=False):
    for key in apt_pkg.config.keys():
        name = key.rstrip(':')
        if refresh_only and not name.lower().startswith('apt::update::'):
            continue
        if any(word in name.lower() for word in ('invoke', 'install-pkgs', 'pre-configure')):
            if name not in HOOKS or any(value not in HOOKS[name] for value in apt_pkg.config.value_list(name)):
                raise MaintenanceError('maintenance_hook_policy')
    for filename in HOOK_FILES:
        if Path(filename).exists():
            file_digest(filename)
