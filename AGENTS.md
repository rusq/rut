# Repository Guidelines

## Project Structure & Module Organization

This Go module (`github.com/rusq/rut`) provides command-line utilities. The executable entry point is `cmd/rut/main.go`. Command implementations live in `cmd/rut/internal/dosomething`, shared configuration in `cmd/rut/internal/cfg`, and reusable command/help infrastructure in `cmd/rut/internal/golang/{base,help}`. Go build-tag files (`*_debug.go` and `*_release.go`) separate debug-only behavior from release builds. There is no test or asset directory; add tests alongside the package they exercise, using files such as `foo_test.go`.

## Build, Test, and Development Commands

Run these commands from the repository root:

- `go build ./cmd/rut` compiles the CLI.
- `go run ./cmd/rut` builds and runs the CLI from source (the program expects a command argument).
- `go test ./...` runs all package tests; it is also the standard regression check as tests are added.
- `go fmt ./...` formats all Go source files.
- `go vet ./...` checks for common correctness issues.
- `go generate ./...` refreshes generated files, including `statuscode_string.go` (requires the configured `stringer` tool).

For debug-only profiling code, use `go build -tags debug ./cmd/rut` or the equivalent `go run -tags debug ./cmd/rut ...`.

## Coding Style & Naming Conventions

Use standard `gofmt` formatting and idiomatic Go naming: exported identifiers use PascalCase and unexported identifiers use camelCase. Keep package names short and lowercase. Prefer focused packages and return errors to callers. Preserve build-tag separation for debug and release behavior, and do not hand-edit generated files; change their source declarations and regenerate them.

## Testing Guidelines

Use Go’s standard `testing` package and name tests `Test<Behavior>`; use table-driven tests for multiple input/output cases. Keep tests in the package they cover and run `go test ./...` before submitting changes. Add coverage for command argument validation, flag handling, exit statuses, and logging/profile setup when changing those areas.

## Commit & Pull Request Guidelines

This repository has no existing commits, so no established commit convention can be inferred. Use short, imperative commit subjects (for example, `Add bench command validation`) and keep unrelated changes separate. Pull requests should explain the behavior change, identify relevant commands or packages, include test results, and attach CLI output or screenshots when user-facing help or output changes. Mention any required generated-file or tool steps explicitly.

## Security & Configuration Tips

The program refuses to run as root. Configuration can come from flags or environment variables such as `TRACE_FILE`, `LOG_FILE`, `JSON_LOG`, and `DEBUG`; avoid committing logs, traces, profiles, or secrets. Review file paths before enabling trace or profiling output.
