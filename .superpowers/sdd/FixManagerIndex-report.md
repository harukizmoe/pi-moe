# FixManagerIndex Report

Status: implemented and verified.

Changes:
- Added an instance-level mutex to `session.Manager` and locked the `Create`, `Touch`, and `UpdateConfig` index read-modify-write sections.
- Changed `saveIndex` to write a temp file in the session root and atomically replace `index.json` with `os.Rename`.
- Added `TestManagerConcurrentCreatesKeepAllEntries` covering concurrent `Create` calls and verifying `List` returns every created session.

RED evidence:
- `go test ./internal/session -run TestManagerConcurrentCreatesKeepAllEntries -count=1` failed before the implementation with `parse session index ... unexpected end of JSON input`.

GREEN evidence:
- `go test ./internal/session -run 'TestManager.*Concurrent|TestManager' -count=1` -> `ok  	harukizmoe/pimoe/internal/session	0.277s`
- `go test ./internal/session -count=1` -> `ok  	harukizmoe/pimoe/internal/session	0.293s`

Files changed:
- `internal/session/manager.go`
- `internal/session/manager_test.go`
- `.superpowers/sdd/FixManagerIndex-report.md`

Risks: none known.
