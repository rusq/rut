# `rut authloginfo`

`rut authloginfo` analyzes Linux authentication logs and prints a
human-readable report of authentication activity. It scans the primary log
and matching numbered rotations, extracts failed and successful events, and
aggregates failed attempts by IP address, username, and country.

The command supports plain and gzip-compressed rotations and processes them
oldest first. Unreadable or malformed inputs are reported as warnings when at
least one input can be analyzed.

## Usage

```bash
rut authloginfo -log-path /var/log/auth.log
rut authloginfo -log-path /var/log/auth.log \
  -geoip-db /path/to/GeoLite2-Country.mmdb
```

Run `rut help authloginfo` for the full command description and shared
logging and tracing flags.

## Flags

- `-log-path`: primary auth log path (default `/var/log/auth.log`)
- `-geoip-db`: optional MaxMind GeoIP2 or GeoLite2 `.mmdb` database; without
  one, country output is grouped under `Unknown`

## Report

The report contains failed login attempts, root-targeted failures, successful
sessions, attempt counts by IP address, username and country, and summary
totals. Recognized sessions include `sshd`, `sudo`, and `su` PAM session-open
events.

This code is part of `rut` and is licensed under the repository's MIT license.
