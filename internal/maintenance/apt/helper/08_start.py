"""Final local start gate; expiry is never an in-flight installation timeout."""
def start_allowed(start):
    fields = {'job_id', 'attempt_id', 'boot_id', 'not_after_ms'}
    uuid = r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
    if (not isinstance(start, dict) or set(start) != fields or
            any(not isinstance(start[k], str) or not re.fullmatch(uuid, start[k])
                for k in ('job_id', 'attempt_id', 'boot_id')) or
            type(start['not_after_ms']) is not int):
        raise MaintenanceError('maintenance_start_invalid')
    boot = Path('/proc/sys/kernel/random/boot_id').read_text().strip()
    now = time.clock_gettime_ns(time.CLOCK_BOOTTIME) // 1000000
    if (boot != start['boot_id'] or start['not_after_ms'] <= now or
            start['not_after_ms'] - now > 60000):
        raise MaintenanceError('maintenance_start_expired')
