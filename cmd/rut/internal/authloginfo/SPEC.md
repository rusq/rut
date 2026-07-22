# Auth Log Analyzer Specification

## Goal

Implement the `rut authloginfo` command to analyze Linux authentication logs and produce a human-readable report of authentication activity, with emphasis on:

- failed login attempts
- failed attempts targeting `root`
- successful authenticated sessions
- per-IP activity
- per-username invalid-login activity
- per-country aggregation for remote IPs

## Scope

The command analyzes SSH- and authentication-related events from system auth logs and summarizes them as tabular output plus summary totals.

The target input set includes the primary auth log and any discovered rotated variants, including compressed rotations.

Examples:

- `/var/log/auth.log`
- `/var/log/auth.log.1`
- `/var/log/auth.log.2.gz`
- additional matching rotations if present

This specification assumes `/var/log/auth.log` as the primary log and requires support for discovered rotated logs alongside it.

## Runtime Requirements

- Language: Go
- Intended environment: Linux system with `/var/log/auth.log`
- Third-party libraries may be used for local MaxMind database lookup and table rendering.

## Functional Requirements

### 1. Input Handling

The program must:

- discover and read `/var/log/auth.log` plus rotated logs matching the same basename
- support both plain-text and gzip-compressed rotated logs
- process each selected file sequentially, line by line
- tolerate unrelated log lines without failing

At minimum, rotated log discovery must support names of the form:

- `auth.log.1`
- `auth.log.<N>`
- `auth.log.<N>.gz`

where `<N>` is a positive integer.

The program must ignore unrelated files that do not match the auth log rotation pattern.

If the primary log file and no matching rotated logs can be opened, the program must fail with a clear, user-facing error message.

If some rotated logs are unreadable or malformed while others are readable, the program should continue with the readable inputs and report the skipped files.

### 1.1 File Ordering

When multiple log files are processed, the program must use a deterministic order.

Recommended order:

1. oldest rotated logs first
2. newer rotated logs next
3. current `/var/log/auth.log` last

For example:

1. `auth.log.3.gz`
2. `auth.log.2.gz`
3. `auth.log.1`
4. `auth.log`

This ordering preserves chronological flow for append-only rotated logs and should be used unless the implementation can derive a more reliable timestamp-based ordering.

### 2. Event Types

The program must recognize at least these event classes:

- failed login attempt for an invalid username
- failed login attempt for `root`
- successful authenticated session

The implementation should use explicit parsing rules based on common auth log formats rather than relying on brittle adjacency assumptions between separate lines.

### 3. Failed Login Attempts

The program must identify failed login attempts from auth log entries that indicate authentication rejection.

For each failed login event, capture when available:

- timestamp
- username
- source IP address
- source port

#### Username Classification

Failed attempts should distinguish:

- invalid/nonexistent usernames
- attempts against valid usernames when detectable

At minimum, invalid-user events must be counted by username.

#### Root Classification

A failed login attempt must be classified as a root-targeted attempt when the attempted username is `root`, based on the actual event data for that attempt.

This classification must not depend on the contents of unrelated neighboring lines.

### 4. Successful Sessions

The program must identify successful authenticated session-open events.

For each successful event, capture when available:

- timestamp
- authenticated username
- session type or origin, such as:
  - `sshd`
  - `sudo`
  - `su`

Only true successful session-open events should be included. The parser should avoid false positives from unrelated messages that happen to contain similar substrings.

### 5. IP Address Aggregation

The program must aggregate activity by source IP address.

The per-IP counts should reflect authentication-related attempts/events only, not arbitrary IP-like tokens found in unrelated log lines.

At minimum:

- failed remote login attempts must contribute to per-IP counts

If the implementation includes successful remote logins in IP totals, that policy must be applied consistently and documented.

### 6. Username Aggregation

The program must produce per-username counts for failed login attempts.

At minimum:

- invalid usernames must be counted

If valid-username failures are also parsed, they should be included under the attempted username.

### 7. Country Aggregation

The command must optionally enrich unique remote IP addresses with country data using a local MaxMind GeoIP2 or GeoLite2 database.

#### Requirements

- perform at most one lookup per unique IP address per run
- cache lookup results in memory for the duration of the run
- aggregate per-country counts from the per-IP counts

#### Failure Handling

Country lookup failures must not terminate the analysis.

If a lookup fails due to an invalid address, missing record, or malformed
database data, the command must assign a fallback country label such as:

- `Unknown`

and continue.

Lookups must avoid repeated database queries for the same IP during a run.

## Parsing Requirements

### Timestamp

Each parsed event must include the timestamp as it appears in the log, when available.

For standard syslog-style auth logs, this is typically:

- `Mon DD HH:MM:SS`

If a timestamp cannot be parsed from an otherwise valid event, the implementation may:

- omit the event, or
- record a placeholder such as `N/A`

but the choice must be consistent.

### Failed SSH Example Patterns

The implementation should correctly parse common patterns such as:

- `Invalid user <username> from <ip> port <port>`
- `Failed password for invalid user <username> from <ip> port <port> ssh2`
- `Failed password for root from <ip> port <port> ssh2`
- `Failed password for <username> from <ip> port <port> ssh2`

The parser may support additional patterns, but these are the minimum expected forms.

### Successful Session Example Patterns

The implementation should correctly parse common patterns such as:

- `pam_unix(sshd:session): session opened for user <username> by ...`
- `pam_unix(sudo:session): session opened for user <username> by ...`
- `pam_unix(su:session): session opened for user <username> by ...`

The session type should be derived from the event itself, not guessed from surrounding lines.

## Data Model

The rewrite should maintain clear internal representations for:

- failed attempts
- root-targeted failed attempts
- successful sessions
- per-IP counts
- per-username counts
- IP-to-country cache
- per-country counts

### Recommended Event Shapes

Failed attempt record:

- `timestamp`
- `username`
- `source_ip`
- `source_port`
- `is_root_target`
- `failure_kind`

Where `failure_kind` may be values such as:

- `invalid_user`
- `bad_password`
- `other_failure`

Successful session record:

- `timestamp`
- `username`
- `session_type`

The exact in-memory structure is not prescribed, but the output must contain equivalent information.

## Output Requirements

The program must print a report to standard output containing:

1. failed login attempts table
2. failed login attempts for root table
3. successful login attempts table
4. IP address attempt counts table
5. username attempt counts table
6. country attempt counts table
7. summary totals

### Required Table Columns

Failed login attempts:

- `Time`
- `User`
- `IP Address`
- `Port`

Failed login attempts for root:

- `Time`
- `User`
- `IP Address`
- `Port`

Successful login attempts:

- `Time`
- `User`
- `Session Type`

IP address attempt counts:

- `IP Address`
- `Attempt Count`

Username attempt counts:

- `Username`
- `Attempt Count`

Country attempt counts:

- `Country`
- `Attempt Count`

### Ordering

Unless there is a product reason to preserve insertion order, aggregate tables should be sorted for readability.

Recommended ordering:

- event tables: chronological file order
- count tables: descending count, then ascending key for tie-breaking

## Summary Totals

The report must include at least:

- total processed log lines
- total successful login attempts
- total failed login attempts
- total failed login attempts for root

Definitions:

- `total processed log lines`: actual number of lines read from all processed log files combined
- `total successful login attempts`: number of parsed successful session events
- `total failed login attempts`: number of parsed failed login events
- `total failed login attempts for root`: subset of failed login attempts where username is `root`

Totals must not double-count root failures inside overall failed-login totals.

## Correctness Constraints

The rewrite must avoid these classes of errors:

- deriving failed-attempt IP/port from a previous unrelated line
- classifying root attempts based on neighboring lines instead of the current event
- counting arbitrary IP-like tokens from unrelated log lines as auth attempts
- treating non-200 geolocation responses as successful lookups
- allowing geolocation failures to crash the program
- reporting event totals that do not match the actual parsed data

## Error Handling

The program must handle the following gracefully:

- missing log file
- unreadable log file
- unreadable rotated log
- invalid gzip-compressed rotated log
- malformed or unexpected log lines
- geolocation request failure
- geolocation response parsing failure

Graceful handling means:

- produce a clear error for unrecoverable input problems
- continue analysis where reasonable for recoverable parsing/network problems

## Non-Functional Requirements

- Log scanning should be linear in the number of input lines.
- Country lookups should occur only after unique relevant IPs are known, or otherwise be effectively deduplicated.
- The implementation should avoid unnecessary memory growth beyond storing parsed events and aggregates.
- The parser should be readable and testable, with event-detection logic separated from presentation logic.

## Recommended Structure

The rewrite should separate:

- log parsing
- event normalization
- aggregation
- country enrichment
- output formatting

This is not mandatory, but a modular design is strongly preferred so the parser and aggregators can be tested independently.

## Acceptance Criteria

A conforming implementation should satisfy all of the following:

- It reads `/var/log/auth.log` and discovered rotated auth logs when present.
- It supports both plain-text and `.gz` rotated logs.
- It processes multiple log files in deterministic oldest-to-newest order.
- It extracts failed attempts with username, IP, and port from the same event line when that data exists.
- It correctly identifies failed attempts targeting `root`.
- It extracts successful session-open events for `sshd`, `sudo`, and `su`.
- It produces the six required tables and the required summary totals.
- It counts only authentication-relevant events in aggregation tables.
- It performs no more than one geolocation lookup per unique IP per run.
- It survives geolocation failures by using `Unknown` instead of terminating.
- It reports actual processed line count and non-double-counted event totals.
