# Disk space

Reads space usage for each supported local filesystem and compares it with warning and critical thresholds. Writable filesystems determine attention; mounts that share capacity are identified. It does not check inodes, quotas or network filesystems.

Python SDK v1. Verified support: Debian 13 on amd64. No maintenance action is performed.
Settings and individual server inheritance are supplied by the TinyWarden engine.
