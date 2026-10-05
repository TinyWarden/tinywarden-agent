# Memory availability example

An independently installable Python SDK v1 skill for Debian 13 on amd64. It reads
the kernel's MemTotal and MemAvailable values through the bounded host broker.
No app or agent source modification, command profile or rebuild is required.

Used memory means total minus available memory; this accounts for reclaimable
cache and is not a measurement of individual processes. Thresholds describe this
local snapshot only. The example does not change host settings or contact a network.

Its settings are a warning percentage, a critical percentage and an interval.
The critical percentage must be greater than the warning percentage. Individual
server fields can override the global defaults while the other fields inherit.

This example is not installed or enabled automatically with official skills.
