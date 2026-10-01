# Agent Coding Guidelines

## 1. Naming & Documentation

- Self-Descriptive Names: Use clear, unambiguous identifiers for variables, functions, structs, and interfaces (e.g., `isStripeWebhookVerified`).
- Crisp Function Docs: Limit docstrings to 1 line explaining intent or critical invariants. Omit comments that restate what the code already says.
- Targeted Inline Comments: Use inline comments strictly for domain edge cases, formulas, or non-obvious workarounds.

## 2. Control Flow & State Design (Raft Principles)

- State Space Reduction: Minimize special-case handling and edge-case branching. Design systems with fewer possible states so fewer checks are required.
- Guard Clauses / Early Returns: Handle errors and preconditions at the top; avoid deep nested `if/else` structures.
- Unidirectional Data Flow: Establish a single source of truth and a linear data path rather than multi-way reconciliation.
- Randomization over Complex State: Use randomized backoffs or timeouts instead of intricate negotiation or retry state machines where suitable.

## 3. Architecture & Code Structure

- Top-to-Bottom Flow over Deep Call Chains: Avoid multi-tier indirection where function A calls function B which calls function C (each carrying its own complexity). Prefer cohesive, readable functions that read top-to-bottom even if they run longer.
- Pragmatic Duplication: Prefer a little duplication over premature abstractions, deep call stacks, or micro-functions that fragment the mental model.
- Clean Boundary Separation: Keep entry points (`cmd/`) strictly for setup, dependency wiring, and graceful shutdown. Delegate all actual domain and business logic to its respective domain package.
- Strict Type Schemas: Use concrete structs, interfaces, and types. Never pass generic dynamic objects (`any`/`map[string]any`) that require multi-file tracing to inspect shapes.
- Colocated Tests: Place unit tests directly alongside implementation files to provide an immediate, self-contained verification loop.
- Zero Dead Code: Keep files clean of commented-out legacy code, unused imports, or redundant boilerplate.

## 4. State & Invariants

- Make Invalid States Unrepresentable: Encode invariants in types and APIs rather than relying on scattered runtime checks.
- Centralize State Mutation: Give important state a clear owner and a small, explicit set of mutation points.
- Single Authoritative Representation: Avoid multiple independent representations of the same fact; derive secondary state where practical.
- Prefer Monotonic State: Where the domain permits, important state should move in one direction; make reversals explicit.

## 5. Dependencies & Side Effects

- Separate Decisions from Effects: Keep validation, calculations, and state-transition decisions separate from I/O and other side effects.
- Explicit Dependencies: Prefer explicit parameters and dependency injection over hidden globals, implicit initialization, or service locators.

## 6. Correctness & Testability

- Make Invariants Executable: Enforce important assumptions through types, validation, assertions, or tests rather than comments alone.
- Preserve Error Causality: Wrap errors with useful context while retaining their original cause.
- Deterministic by Default: Isolate unavoidable nondeterminism such as time, randomness, scheduling, and network behavior.

## 7. Lifecycle & Ownership

- Explicit Ownership: Every resource, goroutine, lock, transaction, timer, and subscription should have an obvious owner and lifecycle.
- Minimize Temporal Coupling: Avoid APIs that require callers to perform undocumented sequences of operations.

## 8. WanderSort specifics

- Entry point is `internal/cli` (wiring and rendering only); domain logic lives in its `pkg/` package.
- `any`/`map[string]any` only where the data is genuinely open-ended: exiftool JSON, log attrs.
- File states change only through `pkg/db/state.go`.
- No package-level vars as test seams; put the dependency on `Options` or a constructor.
- Bounded worker pools only; no fire-and-forget goroutines.
- No history in comments or docs ("used to", "was a bug", issue/spec ids). Git holds history.
- No personal or real-world example values (devices, cities, people, paths) in comments or docs; use placeholders like `<city>`, `<device>`.
- No license headers in source files; the license lives only in `LICENSE`.

## 9. Go conventions

- Wrap errors with `fmt.Errorf("context: %w", err)`.
- `ctx` first for DB, network and long-running work; not for plain local syscalls.
- Upserts (`INSERT … ON CONFLICT`) over select-then-insert.
- Log through `pkg/logger`, never stdlib `log`. Log keys are camelCase.
- Named constants for thresholds and magic values.

## 10. Tests

- Test business logic only: algorithms, branches, data-loss and ordering paths. No tests for trivial data flows, getters or one-line wrappers.
- Table-driven for cases of one shape; helpers call `t.Helper()`.

Respond terse like smart caveman. All technical substance stay. Only fluff die.

Rules:

- Drop: articles (a/an/the), filler (just/really/basically), pleasantries, hedging
- Fragments OK. Short synonyms. Technical terms exact. Code unchanged.
- Pattern: [thing] [action] [reason]. [next step].
- Not: "Sure! I'd be happy to help you with that."
- Yes: "Bug in auth middleware. Fix:"

Switch level: /caveman lite|full|ultra|wenyan
Stop: "stop caveman" or "normal mode"

Auto-Clarity: drop caveman for security warnings, irreversible actions, user confused. Resume after.

Boundaries: code/commits/PRs written normal.
