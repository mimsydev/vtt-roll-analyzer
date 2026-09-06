# Session Log Analyzer — Design Doc & Roadmap

**Status:** Draft, in progress
**Last updated:** 2026-09-05

## 1. Goals & Non-Goals

### Goals
- Build a Go service that ingests TTRPG session log files and produces aggregate dice-roll statistics (crit/fumble rates, per-player trends, etc.)
- Establish a clean boundary between **log parsing** (format-specific) and **data aggregation/display** (format-agnostic), so new log formats can be added later without touching the aggregation layer
- Use this project to deliberately exercise: worker pools, channels/`select`, `sync` primitives (WaitGroup, Mutex/atomic), and `context` for cancellation/timeouts
- Ship a working Roll20 log reader as the first (and for now, only) concrete implementation of the reader interface

### Non-Goals (for this phase)
- No Arena, Foundry, or other platform readers yet — the interface must *support* adding them later, but we are not building them now
- Not focused on a polished UI/frontend — CLI or minimal HTTP output is sufficient
- Not optimizing for massive scale (this is a personal/small-group tool, not a SaaS product)

## 2. Open Design Decisions

> Each decision below should be updated with a ✅ **Decided** marker and rationale once settled. Keep the discarded options and *why* they were discarded — that's the point of a design doc.

### 2.1 `LogReader` interface signature
**Status:** ✅ Decided — Option A (whole-file parse)

```go
type LogReader interface {
    Parse(r io.Reader) ([]RollEvent, error)
}
```

**Rationale:** Two distinct concurrency granularities were being conflated —
(a) one-worker-per-file (across the batch), which is the pool's job, vs.
(b) splitting a *single* file's parse across multiple workers, which only makes sense for
splittable (line-delimited) formats and only pays off once sequential parse time is long
enough to dwarf split/merge coordination overhead (roughly tens-to-hundreds of MB for typical
per-line regex parsing).

Roll20's saved chat log is HTML — hierarchical/sequential DOM structure, not safely splittable
at arbitrary byte offsets. Combined with realistic Roll20 session log sizes (KB–low-MB range),
neither streaming nor intra-file splitting is justified. The reader stays a pure,
concurrency-agnostic function from bytes to `[]RollEvent`; all channel/goroutine concerns live
in the worker pool layer, one goroutine per file.

### 2.2 `RollEvent` schema
**Status:** ✅ Decided

```go
type RollEvent struct {
    Timestamp  time.Time
    Roller     Roller     // who rolled — see struct below
    Formula    string     // e.g. "1d20+5"
    Result     int        // total after modifiers
    Rolls      []int      // individual die faces — required for crit/fumble stats
    Label      string     // free-text purpose, e.g. "Roll for Initiative"
    Visibility Visibility // see enum below — replaces earlier boolean IsGM
    Source     string     // originating file/session identifier
}

type Roller struct {
    Name string
    Role Role
}

type Role string

const (
    RoleGM      Role = "gm"
    RolePlayer  Role = "player"
    RoleUnknown Role = "unknown" // reader couldn't determine this from the source
)

type Visibility string

const (
    VisibilityPublic      Visibility = "public"
    VisibilityGMRoll      Visibility = "gmroll"      // roller + GM see result
    VisibilitySecret      Visibility = "secret"      // GM only; roller gets confirmation, no value
    VisibilitySuperSecret Visibility = "supersecret" // GM only; roller sees nothing
)
```

**Rationale:**
- **`Rolls` + `Result` both kept.** `Result` is post-modifier and useless for raw stats (a
  12 total on `1d20+5` tells you nothing about crit/fumble). `Rolls` (raw die faces) is required
  for crit/fumble detection; `Result` remains useful for modifier-inclusive analysis later
  (DPR trends, hit/miss against a threshold).
- **`Visibility` enum, not a boolean `IsGM` flag.** Roll20 alone has at least three GM-related
  visibility tiers — `/gmroll` (roller + GM see result), `/sr` secret (GM sees result, roller
  gets confirmation only), `/ssr` super-secret (GM sees result, roller sees nothing). This is a
  closed categorical property, not a binary one. A bool would either lose the distinction between
  tiers or invite sibling booleans (`IsSecret`, `IsSuperSecret`) that can represent invalid/
  contradictory combinations. A typed enum models the actual domain and extends cleanly if a
  future reader (e.g. Foundry, with its own public/private/blind/self modes) needs different
  states.
- **`Roller` is its own struct (`Name` + `Role`), not a flat string.** `Role` (who rolled) is
  orthogonal to `Visibility` (who can see the result) — a GM can make a fully public NPC attack
  roll, and a player can send a `/gmroll` to hide their own result. Conflating identity and
  visibility into one field was the original mistake; splitting them out fixes it. Nesting as a
  struct (rather than flat `Roller string` / `RollerRole Role` fields on `RollEvent`) anticipates
  the `Roller` concept growing further attributes later (e.g. a stable player ID), avoiding a
  field-by-field migration.
- **`RoleUnknown` included deliberately.** We have not yet inspected real Roll20 chat-log HTML to
  confirm GM-authored messages are reliably distinguishable from player ones (e.g. via a CSS
  class or marker). The enum must allow "reader couldn't determine this" rather than forcing a
  guess. NPC rolls (GM rolling as a monster) are a known real case that would otherwise get
  wrongly lumped into `RoleGM`, skewing "GM's own dice luck" stats — deliberately **not** added
  as a fourth enum value yet, pending inspection of actual log output; `RoleUnknown` covers it
  safely for now.
- **Reader stays policy-free.** The reader parses and tags whatever visibility/role it observes in
  the source — it does not decide inclusion/exclusion. The aggregator decides whether
  `VisibilitySecret`/`VisibilitySuperSecret` events count toward standard stats or get reported
  separately as GM-only activity. Keeps parsing and policy cleanly separated.

### 2.3 Reader registry / plug-in point
**Status:** Not yet discussed

How does the system know which `LogReader` to use for a given file? Options to evaluate later:
- A `Detect(path string) (LogReader, bool)` step per reader
- A simple map of format-name → constructor, chosen explicitly by the caller (e.g. CLI flag `--format roll20`)

### 2.4 Delivery surfaces & batch vs. persistent service shape
**Status:** ✅ Decided

**Surfaces:** CLI and a web-facing API are the two product delivery surfaces. GitHub Actions is
used for project CI/CD tooling only (see Section 3.4) — **not** as a third data-processing
entrypoint.

**Why Actions was rejected as a product surface:** Actions triggers on repo events (push, PR,
schedule). For it to process real dice logs, those logs would need to live in a git repo and be
pushed to trigger a run — nobody's actual workflow is "commit my Roll20 exports to git so CI can
analyze them." That invents a reason to use the repo as a data pipe rather than fitting the data's
natural shape (files on disk for the CLI, an upload for the API) — the same trap flagged earlier
when scoping out the token-generator idea.

**Shared architecture:** all surfaces are thin adapters around one core engine package with no
knowledge of how it was invoked:
```
/internal/engine   — worker pool, reader interface, aggregator (the core project)
/internal/reader   — Roll20 reader implementation
/cmd/cli           — CLI adapter (args in, stdout report out)
/cmd/server        — HTTP adapter (upload in, JSON/dashboard out)
```
Each adapter: (1) gets input files from its own context (CLI args / repo checkout / HTTP upload),
(2) calls the core engine, (3) renders the result in its own shape.

**Resolves the earlier batch-vs-persistent tension:** the engine itself is always a one-shot,
transient batch run — start pool, process given files, drain, return results. The web server is
the only persistent *process*, but it does not hold a persistent pool across requests; it spins up
a fresh transient engine run per request, same as the CLI does per invocation. No need to solve
"long-lived pool serving many unrelated clients safely."

## 3. Project Setup & Dependencies

### 3.1 Package layout
```
/internal/engine   — worker pool, reader interface, aggregator (core, invocation-agnostic)
/internal/reader   — Roll20 reader implementation (and future format readers)
/cmd/cli           — CLI adapter
/cmd/server        — HTTP/web API adapter
```
Adapters under `/cmd` are thin: gather input, call `/internal/engine`, render output. All
concurrency/business logic lives in `/internal/engine`, testable without any adapter involved.

### 3.2 Dependencies

**Status:** ✅ Decided

- **HTML parsing:** `golang.org/x/net/html` (manual tree-walk over the parsed `*html.Node`).
  Rejected `goquery` (CSS-selector convenience over the same underlying parser) and regex (HTML
  isn't regular; Roll20's export structure varies across plain rolls, templated character-sheet
  rolls, and macros — too brittle). Chose the lower-level option deliberately: it's more verbose,
  but the manual traversal is real educational value the project is meant to deliver, whereas a
  selector engine would hide exactly the mechanics worth learning here.
- **CLI framework:** stdlib `flag`. The CLI has one primary action (`analyze --dir ./logs
  --format roll20`) with no need for subcommands, nested flag scopes, or shell completion —
  `cobra`'s subcommand framework would be solving a problem this project doesn't have.
- **HTTP:** stdlib `net/http` with the Go 1.22+ `ServeMux` (method+path pattern routing). API
  surface is small (upload endpoint, results-fetch endpoint) — a router (`chi` etc.) earns its
  keep with many routes and middleware chaining, which isn't the case here yet. Reference:
  https://pkg.go.dev/net/http#ServeMux
- **Test fixtures:** stdlib `testdata/` directory convention — ignored by the `go build`/`go vet`
  toolchain, loaded via `os.ReadFile("testdata/...")` in table-driven tests. Reference:
  https://pkg.go.dev/cmd/go#hdr-Testdata_directories

### 3.3 CLI vs. server, restated
See Section 2.4 — both are thin adapters over `/internal/engine`; no separate design needed here
beyond package layout above.

### 3.4 CI/CD tooling (GitHub Actions)

**Status:** ✅ Decided — Actions used for project tooling, not as a product surface (see 2.4 for
rationale on why Actions was rejected as a third data-processing entrypoint).

Planned workflows:
- **On every push/PR:** `go vet ./...`, `go test ./...`, and a linter (e.g. `golangci-lint`) —
  standard CI hygiene
- **On tag/release:** cross-compile the CLI binary for a few platforms and attach to a GitHub
  Release (manual matrix build, or `goreleaser` as a learning target)
- **Integration check:** a workflow that runs the built CLI against the fixture Roll20 logs
  checked into the repo (see Section 4, step 3) and asserts output matches expected stats — Actions
  exercising the project's own tool as a correctness check, not as the delivery mechanism to a
  real end user

These are added as their own roadmap items in Section 4 rather than blocking core engine work.

## 4. Build Roadmap

> Ordered, each item scoped to roughly one sitting. Will be refined as design decisions land.

1. [x] Finalize `LogReader` interface and `RollEvent` schema (Section 2.1–2.2)
2. [x] Set up Go module + package structure (Section 3.1)
3. [x] Acquire/produce sample Roll20 chat log export(s) for development fixtures
4. [ ] Implement Roll20 `LogReader`: parse saved chat-log HTML into `[]RollEvent`
5. [ ] Unit tests for the Roll20 reader against fixture files
6. [ ] Design and implement the worker pool: bounded goroutines pulling file paths from a job channel, each invoking the appropriate `LogReader`
7. [ ] Wire up `context` for cancellation (e.g. user interrupts a batch run) and `sync.WaitGroup` for clean shutdown/drain
8. [ ] Aggregation layer: fan-in results from all workers into combined stats (crit rate, fumble rate, per-player breakdowns)
9. [ ] `cmd/cli` adapter: CLI summary output over the core engine
10. [ ] `cmd/server` adapter: HTTP API (upload → JSON/dashboard) over the same core engine
11. [ ] CI workflow: `go vet` + `go test` + lint on push/PR (Section 3.4)
12. [ ] CI workflow: integration check running the built CLI against fixture logs (Section 3.4)
13. [ ] CI workflow: release build/cross-compile on tag (Section 3.4)
14. [ ] Stretch: reader registry/plug-in point so a second format could be added without touching the pool or aggregator (Section 2.3, deferred)

## 5. Notes / Parking Lot

- Roll20 chat logs are saved manually (File → Save As / Ctrl+S) as a multi-file webpage, not a clean text export — reader will need to parse HTML, not plain text
- Secret/`/gmroll` visibility means a non-GM-saved log may be missing some rolls entirely — GM's own saved log is the most complete source
- Confirm current Roll20 export behavior against https://help.roll20.net/hc/en-us before finalizing the reader's expected input format, since this was sourced from community forum reports rather than official docs
