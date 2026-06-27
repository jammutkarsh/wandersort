> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# Agent Coding Guidelines

This document outlines the standard coding patterns, style, and rules that AI agents must follow when contributing to this codebase.

## 1. Testing Strategy

- **Algorithm-focused Tests**: Unit tests must focus on algorithmic correctness, edge cases, concurrency constraints, memory limits, etc.
  - *Example:* Testing `walkRoot()` for correct traversal of a directory tree, including edge cases like symlinks, permission errors, and empty directories.
- **Skip Trivial Data Flows**: Avoid testing simple data parsing or standard read/write flows unless it contains complex business logic.
  - *Example:* Avoid writing tests for a simple HTTP handler that directly calls a database abstraction and returns JSON without any conditional logic.
- **Table-driven Tests**: Use [TableDrivenTestsarrays](https://go.dev/wiki/TableDrivenTests) to group related test cases of similar types into a single test function. Avoid creating multiple separate single-case test functions for similar logic.
  - *Example:*

    ```go
    func TestClassifyName(t *testing.T) {
        tests := []struct {
            name     string
            input    string
            expected string
        }{
            {"valid name", "john", "person"},
        }
        for _, tt := range tests {
            t.Run(tt.name, func(t *testing.T) { /* test logic */ })
        }
    }
    ```

- **Use Test Helpers**: Extract repetitive test setups into helper functions and use `t.Helper()` to ensure test failure line numbers remain accurate.
  - *Example:*

    ```go
    func setupTestDB(t *testing.T) *sql.DB {
        t.Helper()
        // setup and return db
    }
    ```

## 2. Documentation and Comments

- **Concise Language**: Code comments must be minimal, concise, and straightforward. Get straight to the point.
  - *Example:*
    `// Good: Computes the user's age in days.`
    `// Bad: This function takes a user object and calculates their age by subtracting...`
- **Technical Jargon**: Use relevant common technical jargon to minimize word count. Do not explain standard computer science terms (e.g., "hash function").
  - *Example:* Say "Computes the SHA-256 hash" instead of explaining what a hash function is.
- **References**: Add references or documentation links for uncommon terms which cannot be found on Wikipedia.
  - *Example:* `// Uses the Aho-Corasick algorithm (https://en.wikipedia.org/wiki/Aho-Corasick_algorithm).`
- **Public vs. Private Functions**:
  - **Public functions** require a docstring explaining what the function does, its parameters, and its return value. This rule only applies to functions performing complex computations; simple helpers (e.g., parsing a string to a positive int) do not need this.
    - **Constructors** *(an exception)*: Constructors like `New()` functions do not need a docstring because their purpose is obvious from context.
  - **Private functions** need a simple comment above the definition explaining their purpose. Private functions with self-documenting names and straightforward logic (< 5 lines) may omit comments.
  - *Example:*

    ```go
    // CalculateTrajectory computes the optimal path for the given mass and initial velocity.
    // It returns the coordinates and any error encountered during calculation.
    func CalculateTrajectory(mass, vel float64) ([]Coord, error) { ... }

    // No docstring needed for simple helper
    func parsePositiveInt(s string) int { ... }
    ```

- **API Documentation**: API handlers and endpoints must include Swagger doc-based comments (e.g., `// @Summary`, `// @Description`) so that Swagger documentation can be automatically generated via the CLI.
  - *Example:*

    ```go
    // HandleReset godoc
    // @Summary Reset all application data
    // @Description Deletes all scan sessions, file registry entries, content groups and group members in a single transaction. Irreversible.
    // @Tags Admin
    // @Produce json
    // @Success 200 {object} ResetResponse
    // @Router /internal/v1/admin/reset [post]
    func (h *Handler) HandleReset(c *gin.Context) { ... }
    ```

## 3. Naming Conventions

- **English-like Semantics**: Package and function names should combine to read like an English phrase.
  - *Example:* `location.Resolver()` is preferred as it clearly states "location resolver". Avoid `loc.GetRes()`.
- **Avoid Abbreviations**: Do not use abbreviations or acronyms in function or struct names unless they are commonly understood.
  - *Example:* Prefer `hashFile()` over `hf()`.
- **Descriptive Variables**: Variables and constants must have descriptive names that clarify their intent. Single-letter variables are only acceptable for common loop indices (`i`, `j`, `k`) or standard conventions (`err` for errors, `ctx` for context).
  - *Example:* `maxRetries` instead of `m`.
- **Generic Function Names**: Keep function names generic enough to allow underlying implementations to change.
  - *Example:* Use `hashFile()` instead of `BLAKE3Hash()`. Caller of `hashFile()` should not need to know the underlying hashing algorithm.

## 4. Code Structure & Best Practices

- **No Magic Values**: Avoid hardcoded numbers or string literals in the logic. Define them as constants with descriptive names and docstrings explaining *why* the value was chosen. This applies to configuration values, thresholds, status strings, and environment variable names. It does not apply to error message strings, SQL DDL in migrations, or standard format specifiers.
  - *Example:*

    ```go
    // DefaultTimeout is chosen based on the p99 response time of downstream services.
    const DefaultTimeout = 500 * time.Millisecond
    ```

- **Error Wrapping**: Always wrap errors with context using `fmt.Errorf("description: %w", err)` to preserve the original error trace and add debugging context.
  - *Example:* `fmt.Errorf("failed to read config file: %w", err)`
- **Context Propagation**: Always pass `context.Context` as the first argument to any function performing blocking I/O, database operations, or long-running tasks.
  - *Example:* `func fetchUser(ctx context.Context, id int) (*User, error)`
- **Bounded Concurrency**: Use bounded worker pools rather than unbounded goroutines to prevent resource exhaustion (e.g., spinning up a fixed number of workers based on `workerCount`). Avoid fire-and-forget goroutines that can lead to unbounded resource usage.
  - *Example:*

    ```go
    sem := make(chan struct{}, 10) // pool of 10 workers
    for _, item := range items {
        sem <- struct{}{}
        go func(item Item) {
            defer func() { <-sem }()
            process(item)
        }(item)
    }
    ```

- **Dependency Injection**: Pass dependencies (like loggers, database connections, and path resolvers) through constructors rather than relying on global state or singletons.
  - *Example:* `func NewService(db *sql.DB, logger *zap.Logger) *Service`
- **Logging**: Use the application's custom logging library (e.g., `h.log.Info()`) instead of the standard library `log` package.
  - *Example:* `h.log.Info("user logged in", "userID", id)` (Avoid `log.Printf()`)
- **Database & Concurrency**: Avoid `SELECT`-then-`INSERT` patterns that can cause race conditions. Instead, rely on atomic operations like `INSERT ... ON CONFLICT DO UPDATE` (upserts).
  - *Example:* `INSERT INTO users (id, name) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET name = $2;`
