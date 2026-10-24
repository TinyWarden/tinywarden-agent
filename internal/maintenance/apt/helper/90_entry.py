def main():
    import sys
    if globals().get('MAINTENANCE_INSPECT') is True:
        inspect_locked()
        return
    if os.geteuid() != 0:
        raise MaintenanceError('maintenance_root_required')
    raw = sys.stdin.buffer.read(65537)
    if len(raw) > 65536:
        raise MaintenanceError('maintenance_request_invalid')
    try:
        request = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        raise MaintenanceError('maintenance_request_invalid') from None
    if not isinstance(request, dict) or set(request) != {'phase', 'plan_sha256', 'start'}:
        raise MaintenanceError('maintenance_request_invalid')
    phase, expected = request['phase'], request['plan_sha256']
    if phase not in ['refresh', 'prepare', 'fetch', 'apply', 'verify']:
        raise MaintenanceError('maintenance_request_invalid')
    if (phase in ('refresh', 'prepare') and expected is not None or phase not in ('refresh', 'prepare') and
            (not isinstance(expected, str) or not re.fullmatch('[0-9a-f]{64}', expected))):
        raise MaintenanceError('maintenance_request_invalid')
    if phase != 'apply' and request['start'] is not None:
        raise MaintenanceError('maintenance_request_invalid')
    if phase == 'apply':
        start_allowed(request['start'])
    else:
        def cancelled(signum, frame):
            raise MaintenanceError('maintenance_cancelled')
        # Safe phases unwind their native child cleanup before returning. A caller
        # cancellation must not SIGKILL the helper and orphan its index/download work.
        signal.signal(signal.SIGTERM, cancelled)
    configure(upgrade=phase != 'refresh')
    if phase in ('refresh', 'prepare'):
        refreshed = strict_refresh(upgrade=phase == 'prepare')
        result = prepare() if phase == 'prepare' else refreshed
    elif phase == 'verify':
        result = verify()
    else:
        result = fetch(expected) if phase == 'fetch' else apply(expected, request['start'])
    emit_result({'schema_version': 1, 'result': result})

def emit_result(value):
    value['receipt'] = globals().get('MAINTENANCE_RECEIPT')
    raw = canonical(value) + b'\n'
    while raw:
        raw = raw[os.write(RESULT_FD, raw):]
    if stat.S_ISREG(os.fstat(RESULT_FD).st_mode):
        os.fsync(RESULT_FD)

if __name__ == '__main__':
    # Keep package-script output off the closed JSON channel, including native writes.
    RESULT_FD = os.dup(1)
    os.dup2(2, 1)
    try:
        main()
    except MaintenanceError as error:
        emit_result({'schema_version': 1, 'error': str(error), 'subjects': error.subjects, 'subject_count': error.subject_count, 'effect': EFFECT})
        raise SystemExit(1)
    except Exception:
        emit_result({'schema_version': 1, 'error': 'maintenance_adapter_failed', 'subjects': [], 'subject_count': 0, 'effect': EFFECT})
        raise SystemExit(1)
