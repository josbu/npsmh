# Lean Refactor Plan

## Scope

This planning round covers Go code only.

- In scope: every directory that currently contains `*.go`.
- Out of scope for this round: `frontend/`, static assets, HTML templates, images, and non-Go build assets.
- Functional behavior is allowed to change during refactor. Legacy behavior is not frozen.
- If behavior is redesigned, update dependent code and the related docs in the same change set.
- The goal is not "fewest files possible". The goal is "fewer weak files, clearer runtime paths, lower copy/goroutine overhead, stronger Go-style cohesion, and code that stays easy to change".

Final integration state for this repository:

- Directory-level `GO_REFACTOR_PLAN.md` work packets were used during implementation and have now been deleted after completion.
- The root status table below is the source of truth for this Go refactor pass.
- Remaining work is final integration review, commit slicing, and release-facing documentation updates when behavior changes, not another round of directory plan writing.

## Legacy Root Plan Intake

The following root-level documents were reviewed and selectively absorbed into this Go-only refactor system:

- Retained as the project-wide code map:
  `REPO_MAINTAINABILITY_PLAN.md`
- Absorbed into the active refactor backlog and then retired:
  `MULTI_VKEY_CLIENT_REWORK_PLAN.md`
  `GO_CODE_AUDIT_CHECKLIST.md`

Legacy docs feed the current plan in only three ways:

- project context / code map
- open refactor backlog and known defects
- audit hotspots and test gaps

They do not introduce binding architecture or behavior restrictions beyond the goals in this root plan.

Explicitly excluded from this intake:

- `WEB_FRONTEND_REFACTOR_PLAN.md`, because frontend is out of scope for this round.
- historical verification/date claims from older docs
- obsolete "do not refactor this area" restrictions
- old hard file-size rules such as `<=500` or `>600 must split`

Useful heuristics extracted from the older docs, as reference rather than hard constraints:

- Prefer explicit dependencies, providers, and root getters over hidden package-level fallback state when that makes runtime ownership clearer.
- Keep defaults dynamic when they are supposed to follow live config/runtime state; do not freeze them into constructor-time snapshots by accident.
- Remove nil-check forwarding wrappers and one-line root helpers when they only add file hops and hide the real runtime sequence.
- Keep real subsystem boundaries when they still express genuine ownership, but do not preserve a split merely because it already exists.
- Preserve test seams that pay for themselves, but do not let test-only indirection dominate production layout.

Legacy docs are source material only. Active Go refactor work for this pass follows this root plan and the completed directory status table below.

## Transitional Legacy Compatibility Boundary

This refactor targets a transitional release, not a long-lived compatibility branch.

Compatibility decisions in this plan are judged against the released / committed old product line, not against the current dirty worktree.

- Unreleased workspace-only compatibility branches are not protected by default.
- Do not preserve behavior merely because it exists somewhere in the current uncommitted workspace.
- If a simpler, cleaner design removes unreleased compatibility debt, prefer the simpler design and repair callers/tests afterward.

Only these legacy surfaces are explicitly allowed to remain during this refactor:

- old JSON config import, concentrated into one clear import/migration path
- minimal old client connection compatibility only where it is required to keep legacy clients usable for:
  TCP tunnels
  domain forwarding

Everything else should be treated as removal or collapse candidates during refactor:

- old web/session/controller compatibility layers
- broad old-client behavior kept for UDP, P2P, extra proxy modes, or side protocols that are not required for TCP tunnel or domain forwarding
- duplicated legacy auth/import/runtime state kept alive after migration/import already has enough information to normalize it
- compatibility scaffolding added purely to preserve old structure, especially when it adds wrappers, copies, goroutines, or extra file splits

Compatibility code that survives this round must stay thin, localized, and obviously temporary.

- Do not spread legacy branches across multiple packages when one boundary package can absorb them.
- Do not preserve a legacy branch merely because it already exists.
- If a legacy import path is retained, normalize it once and clear temporary legacy state instead of carrying parallel fields forever.
- If an old client path is retained, keep only the minimum handshake/connect path needed for the transitional release.
- Do not use compatibility as a reason to keep technical debt in place. If the debt can be removed without violating the reduced boundary above, remove it.
- The former `lib/store` layer has no historical release-compatibility obligation; it may be removed or absorbed freely. The only requirement is that old JSON import still has one clear owner somewhere in the codebase.

The intended end state after this release is removal of the remaining legacy surfaces. Do not create new debt in the name of compatibility now.

## Integration Model

This pass has moved from parallel directory execution to final integration.

- All Go directory work packets are complete and their local plan files are removed.
- Shared files such as `go.mod`, `go.sum`, `README*`, `docs/**`, `conf/**`, and this root plan are now integration-scope files.
- Final review should focus on commit slicing, API/docs consistency, generated artifact cleanup, and one last focused/full test sweep.
- Future follow-up work should use new task-specific plans only when a new large redesign starts; do not recreate per-directory lock docs for this completed pass.

## Design Constraints

- Prefer package-internal consolidation before adding new packages or `internal/` layers.
- Avoid new interface layers unless they remove a real dependency knot.
- Avoid extra memory copies in hot paths, especially in `bridge`, `client`, `lib/conn`, `lib/mux`, `lib/p2p`, `server/proxy`, and `web/routers`.
- Avoid per-object goroutine fan-out. Reuse bounded worker models where concurrency is needed.
- Treat performance as a first-class design target in every refactor pass: reduce memory copies, transient allocations, serialization churn, timer/channel count, goroutine count, and steady-state CPU usage whenever the design can be simplified at the same time.
- Treat race freedom, deterministic sequencing, bounded goroutine lifetime, and deadlock avoidance as design constraints, not polish to revisit after the refactor lands.
- Prefer direct ownership paths that let the runtime do less work, hold locks for less time, and keep smaller live heaps instead of heavier abstraction stacks that only look flexible on paper.
- Prefer readable ownership-based file groupings over file-count or line-count targets.
- A larger file is acceptable when it keeps one runtime path coherent; split only when the file starts hiding state ownership, data flow, or control flow.

## Go-First Refactor Direction

- Follow Go package habits, not Java-style role slicing into swarms of `manager`, `service`, `factory`, `adapter`, and tiny helper files.
- Group code by runtime path, data ownership, protocol boundary, or mutation boundary.
- Prefer concrete types, package-private helpers, and direct calls over interface/facade layering unless a real boundary exists.
- Avoid getter/setter wrappers, pass-through methods, and one-method-per-file organization when they only create jumps.
- Keep exported surface area small; most refactor cleanup should happen inside packages, not by adding more abstraction layers.
- Refactor passes are expected to simplify structure, improve hot-path performance, and fix clear correctness issues together.
- Refactor is allowed to be thorough and behavior-changing. Do not preserve weak structure or weak semantics merely because changing them feels risky.
- If a materially better implementation is discovered during the pass, adopt it fully instead of preserving the current shape out of inertia. The current code is not a design constraint.
- Do not compromise with existing technical debt, awkward sequencing, or compatibility scaffolding just because it already exists; replace it and repair dependents, tests, and docs until the better shape is solid.
- The repository should not be left in a timid halfway state; if a redesign is the cleaner direction, take it and then repair dependents/tests/docs until it is solid.
- The desired outcome of each package pass is buildable, testable, and close to direct commit, not a half-migrated intermediate state.

## Quality Bar And Execution Tempo

- The refactor target for this repository is not "roughly acceptable". The target is a result that feels deliberate, elegant, and internally consistent, with weak structure removed rather than merely rearranged.
- Do not settle for "can be cleaned later" when the same pass can reach a clearer final shape now. Favor end-state quality over temporary compromise unless correctness or integration safety forces a shorter move.
- Default implementation size should be a large coherent slice. Prefer refactoring an entire runtime path, ownership path, or persistence boundary in one pass instead of shaving one tiny fragment at a time.
- When entering a hotspot, fix nearby structural debt, obvious defects, and avoidable performance waste together so the area exits the pass materially better, not just slightly less bad.
- Slow nibbling is not the desired mode. Break work down only when necessary for safe verification or merge coordination; otherwise push through a broader, cleaner redesign in each pass.
- The intended bar is not only "works and is cleaner". The intended bar is a result that is elegant, performant, concurrency-safe, and close enough to final form that another structural cleanup should not be needed soon.
- When a clearly better design appears, finish that stronger design in the same pass instead of landing a halfway compromise and promising to clean it later.
- A refactor that knowingly keeps avoidable copies, background workers, lock contention, or old technical debt for the sake of short-term safety is below the target bar.

## Cross-Cutting Audit Dimensions

Every directory refactor should also act as a logic/defect audit for that package.

- Before editing a directory, trace its entrypoints, shared mutable state, cancellation paths, cleanup paths, and resource ownership.
- Check for logic bugs, bad edge-case handling, inconsistent state transitions, and hidden nil/panic risks.
- Check error propagation, retry behavior, timeout handling, and cancellation propagation.
- Check concurrency safety: lock scope, goroutine lifecycle, stale worker cleanup, and shared state races.
- Explicitly audit race conditions, sequencing hazards, deadlock risk, lock ordering, goroutine lifetime, copy count, allocation rate, and steady-state CPU/heap cost.
- Check resource cleanup for sockets, listeners, files, timers, tickers, contexts, and channels.
- Check hot paths for avoidable copying, repeated cloning, reflection, busy waiting, unbounded loops, and accidental goroutine fan-out.
- Record confirmed defects in the directory plan while refactoring, then fix them in the same package pass where practical.
- Treat safe performance improvements as part of the refactor work, not as a separate optional cleanup backlog.
- Treat existing technical debt as part of scope. Do not preserve obvious debt by default, and do not create fresh debt as the price of moving faster.
- Fix confirmed structural, concurrency, and performance issues in the same pass when practical; do not create a fresh backlog of known debt merely because the old code still functions.
- Run focused package tests after risky changes. Full-repo verification belongs to the integration pass; older checklist dates are not proof for the current worktree.
- Do not treat existing tests as full coverage or proof of correctness. Manual code reading across the touched runtime path is required, because tests can miss edge cases, sequencing faults, concurrency hazards, and stale assumptions.

## Multi-Instance / Runtime-Owner Audit Targets

The old multi-vkey plan contained still-valid hotspots that now belong in the active backlog.

- Same-`vkey` multi-instance behavior remains a cross-package hotspot spanning `bridge`, `client`, `lib/file`, `lib/p2p`, `server`, `server/proxy`, `web/api`, `web/service`, and `web/routers`.
- Review non-conflicting runtime/no-store resource routing while consolidating files. If creator-instance routing is changed, update dependent code and docs in the same pass.
- Review conflicting-resource behavior as a public-rule-plus-owner-pool problem. If per-instance rotation or canonical-owner behavior is redesigned, make it explicit and document it.
- Review keep-alive/backend reuse together with runtime-owner selection so persistent connections do not silently pin traffic to stale instances.
- Review lost-instance cleanup so one dead instance does not accidentally erase healthy siblings unless that broader cleanup behavior is an intentional redesign.
- Treat instance-level stats and connection detail as runtime-scoped by default. If they move into persisted quota/limit semantics, document that as a deliberate behavior change.
- Keep client summary and client-detail API roles explicit. If summary/detail boundaries move, update the API docs in the same change set.

## Project Tree

```text
nps/
├─ bridge/                     [done]
├─ client/                     [done]
├─ cmd/
│  ├─ npc/                     [done]
│  └─ nps/                     [done]
├─ conf/
├─ docs/
├─ frontend/                   [out of scope in this round]
├─ image/
├─ lib/
│  ├─ cache/                   [done]
│  ├─ common/                  [done]
│  ├─ config/                  [done]
│  ├─ conn/                    [done]
│  ├─ crypt/                   [done]
│  ├─ daemon/                  [done]
│  ├─ file/                    [done]
│  ├─ goroutine/               [done]
│  ├─ index/                   [done]
│  ├─ install/                 [done]
│  ├─ logs/                    [done]
│  ├─ mux/                     [done]
│  ├─ p2p/                     [done]
│  ├─ pmux/                    [done]
│  ├─ policy/                  [done]
│  ├─ pool/                    [done]
│  ├─ rate/                    [done]
│  ├─ servercfg/               [done]
│  ├─ serverreload/            [done]
│  ├─ sheap/                   [removed in refactor]
│  ├─ store/                   [removed in refactor]
│  ├─ transport/               [done]
│  └─ version/                 [done]
├─ scripts/
├─ server/
│  ├─ connection/              [done]
│  ├─ p2pstate/                [done]
│  ├─ proxy/                   [done]
│  │  └─ httpproxy/            [done]
│  └─ tool/                    [done]
└─ web/
   ├─ api/                     [done]
   ├─ controllers/             [removed in first refactor pass]
   ├─ framework/               [done]
   ├─ routers/                 [done]
   └─ service/                 [done]
```

## Directory Status

### Core Packages

| Directory | Plan Doc | Plan Status | Implementation Status | Notes |
| --- | --- | --- | --- | --- |
| `bridge` | deleted after completion | completed | done | Bridge runtime and P2P hot path are complete for this pass; config work-status and runtime health scans now flow through `lib/file`'s `DbUtils.RangeHosts`/`RangeTasks`, `config_task.go` is folded back into `config.go`, `runtime_tunnel.go` is folded back into `runtime_tunnel_server.go`, `handshake_runtime.go` is folded back into `handshake.go`, the P2P association/probe/route split is folded into `p2p_runtime.go`, P2P session control plus telemetry now live together in `p2p_session.go`, `runtime_owner_pool.go` is folded back into `client.go`, and focused plus full-repo tests are green |
| `client` | deleted after completion | completed | done | Client launch/control/runtime hot path is complete for this pass; `runtime_tunnel.go` and `client_shutdown_lifecycle.go` are folded into `client.go`, `control_conn.go` is folded into `control.go`, launch build/config/profile satellites are folded into `launch.go` with `launch_resolve.go` kept as the single support file, `p2p_manager_session.go` is folded into `p2p_manager.go`, provider root channel plus transport ownership is folded into `client_p2p_provider_root.go`, `closeConnOnContextDone` now uses `context.AfterFunc`, and package plus full-repo tests pass |
| `cmd/npc` | deleted after completion | completed | done | CLI bootstrap and launch flow is consolidated into `npc.go` + `bootstrap.go` + `launch.go` (+ build-tagged `sdk.go`), legacy long-flag normalization is kept local to the entrypoint, and package plus full-repo tests are green |
| `cmd/nps` | deleted after completion | completed | done | Server bootstrap entrypoint is collapsed into single-file `nps.go`, startup ownership is direct again, and package plus full-repo tests are green |

### Library Packages

| Directory | Plan Doc | Plan Status | Implementation Status | Notes |
| --- | --- | --- | --- | --- |
| `lib/cache` | deleted after completion | completed | done | LRU cache stays in two owner files, but the cache core no longer pays `sync.Map` overhead on top of an existing mutex; the hot path is back to a plain map+list design, and certificate idle eviction now iterates the same ownership structure directly |
| `lib/common` | deleted after completion | completed | done | Shared utility surface keeps its main network/helper files, while tiny support files are folded back into owners: protocol constants plus NTP/time helpers now live in `util.go`, `pprof.go` is folded into `run.go`, and `BytesToNum` no longer allocates through decimal string building; focused and full-repo tests are green |
| `lib/config` | deleted after completion | completed | done | Config parsing stays in one owner file; `multi_account.go` is folded back into `config.go`, the multi-account import path no longer needs a separate file hop, and focused plus full-repo tests are green |
| `lib/conn` | deleted after completion | completed | done | Transport wrapper/copy path is complete for this pass; transient host/task config decode now allocates ids through `DbUtils.NextHostID`/`NextTaskID` instead of `JsonDb` internals, wrapper/link/snappy/flow/traffic/connect-result helpers are folded back into `conn.go`, `udp_bind.go` is folded into `udp.go`, nil/close handling on timeout/websocket/observer paths is hardened, and focused plus full-repo tests are green |
| `lib/crypt` | deleted after completion | completed | done | TLS/OTP utilities stay compact, `client_hello.go` is folded back into `tls.go`, sniffing now enforces a real capture cap with sane defaults instead of over-buffering the first oversized read, and focused tests stay green |
| `lib/daemon` | deleted after completion | completed | done | Runtime reload/daemon lifecycle; unix reload listener is folded into `daemon.go`, `reload.go` is removed, and the package is back to one production file with focused tests green |
| `lib/file` | deleted after completion | completed | done | Large persistence/object layer is complete for this pass; sort reflection removed, legacy import/helpers and migration are consolidated, local store/config snapshot logic absorbed, DB/load lookup satellites folded back into owner files, `obj_misc.go` deleted, target plus MultiAccount normalization/clone logic centralized, JsonDb coalesces deferred bulk persistence, client-resource reverse indexes and owner/manager user-visibility indexes keep scoped reads off full-map scans, config-snapshot export/import repairs indexes and initializes runtime fields, tunnel protocol normalization is shared by create/update/load/import paths, runtime file indexes use atomic-backed coherent swaps, `DbUtils` owns external readiness/id/store/range entrypoints, `file.go`/`store.go` are now `jsondb.go`/`local_store.go`, retained JSON compatibility loads avoid bytes-string-byte round-trips, and focused/race/full-repo tests are green. Future non-JSON database work should attach behind the existing `Store` / `ConfigExporter` / `ConfigImporter` and JsonDb persistence-backend seams, keeping CRUD/runtime callers on typed owner methods instead of leaking database-specific query code into `web`, `server`, or `bridge` |
| `lib/goroutine` | deleted after completion | completed | done | Single-file copy/runtime helper; HTTP probe no longer allocates via header splitting, fragmented method prefixes are detected, and only ingress traffic marks HTTP so reverse-copy races and false clearing are removed; package plus dependent `lib/conn` / `server/proxy/httpproxy` / `bridge` / `server` tests are green |
| `lib/index` | deleted after completion | completed | done | Index helpers kept in the existing two-file layout; map clear paths now reuse existing allocations, `sync.Map` clears avoid per-entry delete churn, and idle-connection cleanup remains explicit and tested |
| `lib/install` | deleted after completion | completed | done | Installer flow remains single-file; file replacement now stages to a temp file and renames atomically, `CopyDir` uses relative-path joins instead of string replacement, and partial-copy clobber risk is removed |
| `lib/logs` | deleted after completion | completed | done | Logger core remains single-file; zap level gating now follows runtime `SetLevel` changes through shared atomic level state while keeping zerolog ownership unchanged, and focused `lib/logs` plus downstream `lib/serverreload` tests are green |
| `lib/mux` | deleted after completion | completed | done | Mux runtime hot path is complete for this pass; `conn_window.go` and `conn_write_bandwidth.go` are folded into `conn.go`, control/map/random plus bandwidth/latency/lifecycle/timing helpers are folded into `mux.go`, `watchConnDone` now uses one multi-source watcher instead of spawning up to three goroutines per mux, legacy mux rate sampling is atomic and oversized `Get` requests are chunked instead of waiting forever, and focused downstream plus full-repo tests are green |
| `lib/p2p` | deleted after completion | completed | done | P2P runtime hot path is complete for this pass; `util.go`, `network_family.go`, `probe_config.go`, `telemetry_export.go`, `telemetry_sink.go`, `session_runtime_flow.go`, and `session_state.go` are folded back into `protocol.go`, `peer_family.go`, `history.go`, and `runtime_session.go`, telemetry/history global store swaps now use atomic owner snapshots instead of lock-guarded hot reads, bridge and packet read interruption use `context.AfterFunc`, retry/spray sleeps tolerate nil contexts, and package/downstream/full-repo tests are green |
| `lib/pmux` | deleted after completion | completed | done | Listener and packet mux now stays in three owner files; `registry.go` is folded into `pmux.go`, `pconn.go` is folded into `plistener.go`, and the first buffered prefix read can now continue into the underlying socket in the same `Read` call |
| `lib/policy` | deleted after completion | completed | done | Matching/runtime code keeps the real matcher files, while thin support layers (`destination.go`, `mode.go`, `source.go`) are folded into one `policy.go` owner file so rule composition no longer lives across multiple tiny files |
| `lib/pool` | deleted after completion | completed | done | Single-file generic pool; random selection no longer serializes on the global `math/rand` lock and stays cheap under shared-read contention, while the rest of the ownership model remains compact |
| `lib/rate` | deleted after completion | completed | done | Limiter state machine keeps its own file, while `meter.go` and `conn.go` are folded back into `rate.go`; the package is back to two meaningful owner files with focused and full-repo tests green |
| `lib/servercfg` | deleted after completion | completed | done | Server config parsing/runtime view keeps the main builders/snapshot owners, while thin accessor/runtime view files are folded back into `accessors.go` and `runtime_management.go`; standalone web, P2P, capability, and management-runtime views now read as two coherent ownership paths and tests stay green |
| `lib/serverreload` | deleted after completion | completed | done | Runtime reload support remains single-file; reload-time NTP synchronization now reuses the existing in-function concurrency gate directly instead of spawning a fresh goroutine on every apply path, and focused plus full-repo tests are green |
| `lib/sheap` | deleted after completion | completed | done | Dead tiny helper package removed entirely; it had no real imports, so keeping a standalone heap package only added repository clutter |
| `lib/store` | removed | completed | completed | Local snapshot/store layer absorbed into `lib/file`; child plan deleted |
| `lib/transport` | deleted after completion | completed | done | OS-specific socket transport; `keepalive.go` is removed, keepalive validation now sits directly with the owning Windows and non-Windows socket setters, and focused tests are green |
| `lib/version` | deleted after completion | completed | done | Version metadata consistency; the supported-version list now derives its latest entry from `VERSION`, removing the old latest-version duplication while keeping package behavior stable |

### Server Packages

| Directory | Plan Doc | Plan Status | Implementation Status | Notes |
| --- | --- | --- | --- | --- |
| `server` | deleted after completion | completed | done | Runtime orchestration is concentrated into five owner files: `engine_startup.go`, `server.go`, `server_flow_runtime.go`, `client_host_list.go`, and `runtime_dashboard_context.go`; list/runtime coordination/task-runtime satellites stay folded back into owners, hot reads remain index-backed, and focused downstream plus full-repo tests are green |
| `server/connection` | deleted after completion | completed | done | Connection runtime is back to one owner file; `shared_mux.go` is folded into `connection_config.go`, listener/shared-mux ownership now reads as one continuous runtime path, and focused plus full-repo tests are green |
| `server/p2pstate` | deleted after completion | completed | done | Registry package remains single-file by design; expiry handling now treats the deadline as expired immediately, cleanup reuses the same session-expiry helper, and focused plus full-repo tests are green |
| `server/proxy` | deleted after completion | completed | done | Proxy hot path is complete for this pass; bounded close helpers are centralized back into owner files, `active_connection_close.go`/`close_parallelism.go`/`socks5_udp_registry_close.go`/`udp_session_close.go` are removed, `tunnel_http_proxy_shutdown.go` is folded into `http_proxy_handler.go`, SOCKS5 associate control now lives with `socks5_handler.go` and wakes via session close callbacks instead of a per-associate wake goroutine, buffered ingress and request-body limiter reservations are refunded if observers reject bytes, and focused downstream plus full-repo tests are green |
| `server/proxy/httpproxy` | deleted after completion | completed | done | HTTP/HTTPS proxy specialization is complete for this pass; runtime server coordination is folded into `httpproxy.go`, limiter IO wrappers are folded into `http.go`, HTTP/3 runtime/context/worker helpers are folded into `https.go`, thin runtime satellites are removed, request-body limiter reservations are refunded if observers reject bytes, and focused downstream plus full-repo tests are green |
| `server/tool` | deleted after completion | completed | done | Tool helpers remain single-file; port selection no longer allocates `rand.Perm` slices or contends on the global `math/rand` lock, using a cheap allocation-free randomized scan instead, and tests stay green |

### Web Packages

| Directory | Plan Doc | Plan Status | Implementation Status | Notes |
| --- | --- | --- | --- | --- |
| `web/api` | deleted after completion | completed | done | API handler layer is complete for this pass; request/response helpers live in `request_helpers.go` and prefer raw-body views where available, actor/resource visibility is concentrated in `actor.go` plus `action_catalog.go`, auth/session/discovery ownership is concentrated in `auth.go`, node/system control handlers live in `node.go`, domain handlers live in `clients.go`, `users.go`, `tunnels.go`, and `hosts.go`, mutation DTO/auth/error/event-policy helpers live in `mutations.go`, mutation responses consume final `web/service` snapshots directly, protected mutation authorization reuses `web/service` authz ownership, request-scoped `nodeActorAccess` owns principal/scope/visibility/protected-action resolution, and Web integration plus race tests are green |
| `web/controllers` | deleted after completion | completed | done | Wrapper package removed; remaining checks moved into `web/service`, router tests call `web/service` directly |
| `web/framework` | deleted after completion | completed | done | Session/request framework; `request_context.go` is folded into `session.go`, request-body capture now keeps one stored copy plus an explicit read-only view path, and the focused `web/framework` / `web/api` / `web/routers` tests are green |
| `web/routers` | deleted after completion | completed | done | Routing/runtime package is complete for this pass; helper satellites are folded back into runtime owners, protected-route catalogs are unified across HTTP and WS, callback/webhook/event flows reuse owner-local executors, runtime persistence and delivery paths cut redundant copies and goroutines, idempotency replay preserves multi-value HTTP headers and uses bounded same-key takeover for hung in-flight requests, request-body parsing uses explicit raw-body views, websocket/event-sink responses tag trusted body modes to skip redundant validation/copies, and Web integration plus race tests are green |
| `web/service` | deleted after completion | completed | done | Service package is complete for this pass; thin auth/client/quota/rate helpers are merged back into owners, `node_control.go`, `backend.go`, `repository.go`, `authorization.go`, `node_runtime.go`, `node_platform.go`, `node_protocol.go`, `resources.go`, `clients.go`, `users.go`, and `services.go` now own their runtime paths directly, service paths reuse narrower repo indexes and deferred flush windows, mutation entrypoints return final detached snapshots, typed-nil/default resolver drift is closed, `services.go` remains the explicit dependency-wiring owner, and Web integration plus race tests are green |

## Current Integration Conclusions

The Go directory refactor pass is complete.

- Every Go directory in the status table is marked `completed` / `done`.
- All directory-level `GO_REFACTOR_PLAN.md` work packets have been removed after the root status was updated.
- The old `lib/store` package is removed and its local store/snapshot role is absorbed into `lib/file`.
- The old `web/controllers` wrapper layer is removed; ownership now lives in `web/service`, `web/api`, and `web/routers`.
- Root-level temporary test logs, coverage files, and local Go cache artifacts have been cleaned and ignored.
- Latest verification for this integration pass includes `go test ./...`, focused race checks for high-risk runtime packages, and a full `go test -race ./...` pass.

This document is now a final integration record for the Go refactor pass, not an active worker-lock plan.

## Final Merge Checklist

Before committing or opening a PR, do the following:

- Review the full diff by package and split commits by coherent ownership area where practical.
- Run `go test ./...` after the final staging set is chosen.
- Re-run targeted race tests for packages touched after the last full verification, especially `lib/file`, `web/api`, `web/service`, `web/routers`, `server/proxy`, `lib/conn`, `lib/mux`, and `lib/p2p`.
- Confirm no root-level `.tmp-*`, `.codex_*`, coverage, or local gocache artifacts are present.
- Confirm public behavior changes are reflected in `docs/reference/**`, `docs/guide/**`, `README.md`, `README.zh.md`, or `CHANGELOG.md` as appropriate.
- Do not recreate per-directory lock docs for this completed pass.

## Future Store And Database Boundary

`lib/file` is the storage owner for this transitional release, but it should not become permanently JSON-file-only.

- Future non-JSON database work should attach behind the existing `Store`, `ConfigExporter`, `ConfigImporter`, and `JsonDb` persistence-backend seams.
- CRUD and runtime callers should keep using typed owner methods such as `DbUtils.RangeUsers`, `RangeClients`, `RangeTasks`, `RangeHosts`, and entity-specific mutation methods.
- Do not leak SQL, KV, ORM, or database-specific query code into `web`, `server`, `bridge`, or `client`.
- Preserve the current in-memory runtime indexes for hot reads unless a future database-backed design proves an equally cheap read path.
- Move toward incremental persistence through owner mutation methods rather than reintroducing broad JSON full-flush behavior into higher layers.
- Keep old JSON config import as a thin transitional import/migration path; do not turn it into a second live storage model.

## Future Compatibility Boundary

This refactor intentionally keeps only transitional compatibility.

- Retained compatibility: old JSON config import and minimal old client connection compatibility for TCP tunnels and domain forwarding.
- Compatibility that survives should stay local, thin, and easy to delete in the next release.
- Do not preserve unreleased workspace-only behavior as compatibility debt.
- After the transitional release, remove the remaining legacy compatibility surfaces rather than spreading them into new abstractions.

## Definition Of Done

A directory is done only when these are true:

- Thin helper files are removed or merged into real owners.
- Runtime paths need fewer cross-file jumps than before.
- Hot paths do not gain extra copies, extra goroutines, or broader locks.
- The package layout is more idiomatic Go and less class-like / Java-style.
- Clear bugs and safe performance issues found during the pass are fixed, not just documented.
- API, config, runtime behavior, and user-visible changes are documented in the same change set.
- Tests are green, but the touched runtime paths have also been manually read and reasoned through because tests are not assumed complete.

## Follow-Up Focus

Do not start another pure file-shuffling pass by default. Future work should be implementation-level and tied to a real runtime or product outcome.

- `lib/file`: database-backed persistence seam, incremental mutation persistence, and eventual removal of transitional JSON compatibility debt.
- `server/proxy`, `bridge`, `client`, `lib/p2p`: runtime-owner semantics, P2P sequencing, cancellation, and old-client compatibility deletion after the transitional release.
- `web/api`, `web/service`, `web/routers`: API/docs consistency and continued removal of compatibility-only management paths.
- `docs/**`: keep release-facing docs synchronized when API/config/runtime behavior changes.
- `WEB_FRONTEND_REFACTOR_PLAN.md`: remains separate and out of scope for this Go refactor pass.

