# Known Windows-only issues

These issues only happen on Windows. None of them is fixed yet. Each one has a
test that checks for the correct behavior, so the test fails until the issue is
fixed. The tests are behind the `winissues` build tag, so the normal
`go test ./...` stays green:

```bash
go test -tags winissues -run TestWinIssue ./internal/checksum ./internal/mcp
```

When you fix an issue, remove its test from the `winissues` file and add it to
the package's normal tests.

Found on Windows 11 with Go 1.27, on branch `pre-release-fixes` (2026-11-10).

| ID    | Severity | Area           | What happens |
|-------|----------|----------------|--------------|
| WIN-1 | Medium   | MCP / query    | While a repository is bound, its folder and `.eulix` can't be moved or renamed |
| WIN-2 | Medium   | MCP, checksum  | A project opened through a directory junction refuses every MCP query, and its checksum scan comes back empty |

---

## WIN-1: a bound repository folder can't be moved

**Test:** `internal/mcp/winissues_windows_test.go`, `TestWinIssue_BoundRepoFolderCannotBeMoved`

- `query.NewRouter` (`internal/query/router_core.go:72`) always creates a
  `DebugLogger`, even when debugging is off. The logger opens
  `.eulix/debug/context_debug.log`, and the subsystem log
  `.eulix/debug/susbstemDebug.log` is opened the same way. Both stay open as
  long as the context builder is alive.
- Windows refuses to rename a folder that contains an open file, so the rename
  fails with "Access is denied". Linux allows it.
- **What users see:** while an editor runs `eulix mcp` with a project bound,
  they can't rename or move the project folder or `.eulix`, and they can't
  delete `.eulix` to start over. It works again after the idle release closes
  the logs, or after the server stops.
- The same applies to `eulix chat` while it runs.
- **Possible fix:** don't open the debug files unless debugging is on. Or open
  the file for each write, the way `llm/client.go` and `cli/helpers.go`
  already do for their debug logs.
- Because of this, the folder-replacement tests in `symlink_fixes_test.go` and
  `pr_gaps_test.go` (`moveAside`) skip on Windows: they can't move a bound
  folder there.

## WIN-2: a project opened through a directory junction doesn't work

**Tests:**
- `internal/mcp/winissues_windows_test.go`, `TestWinIssue_RepoBoundThroughJunctionIsUnusable`
- `internal/checksum/winissues_windows_test.go`, `TestWinIssue_ScanThroughJunctionRootFindsNothing`

Directory junctions (`mklink /J`) are common on Windows. Unlike symlinks, any
user can create one without admin rights. Since Go 1.23, Go treats a junction
as `ModeIrregular`, not as a symlink:

- `filepath.EvalSymlinks` returns the junction path unchanged;
- `os.Lstat` on the junction returns a mode that is not a directory;
- `filepath.WalkDir` does not enter a root that is a junction.

The effects:

- **MCP** (`internal/mcp/helpers.go`):
  - `set_repo_path` keeps the junction path. It records `os.Stat`, which
    follows the junction to the target.
  - Every query compares that record with `os.Lstat`, which looks at the
    junction itself. The two never match, so every query is refused with
    "repository folder … changed after it was bound".
  - `lexicalSearch` returns nothing, because `WalkDir` doesn't enter the root.
- **Checksum** (`internal/checksum/detector.go`, `scan`): when `eulix analyze`
  runs from a path that goes through a junction, the scan finds no files.
  Change detection then sees every file as deleted.
- On Linux, `EvalSymlinks` resolves a symlinked root, so the same setup works.
- **Possible fix:** resolve the root to its real target before pinning it and
  walking it. On Windows, `GetFinalPathNameByHandle` on an opened directory
  handle does this. Keep refusing junctions *inside* the repository: the
  `os.Root` checks and the `WalkDir` skip already do that correctly.

---

## Fixed

- **WIN-3, WIN-4:** the checksum scan and `ServableMatcher` now skip entries
  with the NTFS hidden attribute, as the parser does
  (`checksum.HiddenByAttr`, tested in `internal/checksum/hidden_windows_test.go`).
- **WIN-5:** tests now set `USERPROFILE` as well as `HOME`.

## Coverage gaps (not bugs)

- `TestEmbedderRoundTrip`, `TestEmbedderStartupFailures`,
  `TestEmbedderTimeoutKillsServer` and `TestGetVenvPython` skip on Windows,
  because the fake interpreter is a shell script. The embedder's start, timeout
  and kill paths have no Windows coverage. A fake interpreter built as a Go test
  binary (re-running `os.Args[0]`) would work on every OS.
- `go test -race` needs cgo, which isn't set up on this machine, so data races
  are only checked on Linux CI.

## Checked and found fine

- Opening a repository with a different path case (`C:\Repo` vs `c:\repo`)
  reuses the existing binding.
- Files in a bound repository can still be rewritten and renamed (WIN-1 only
  blocks renaming folders). `embeddings.bin` is read and closed, not kept
  mapped.
- Junctions inside a repository are not followed: the `os.Root` reads and the
  `WalkDir` skip both refuse them.
