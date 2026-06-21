# Agent Coding Guidelines

This document outlines the standard coding patterns, style, and rules that AI agents must follow when contributing to this codebase.

## 1. Testing Strategy

- **Algorithm-focused Tests**: Unit tests must focus on algorithmic correctness and edge cases (e.g., concurrency constraints, memory limits).
- **Skip Trivial Data Flows**: Avoid testing simple data parsing or standard read/write flows unless they contain complex business logic.
- **Table-driven Tests**: Use arrays of structs (table-driven tests) to group related test cases of similar types into a single test function (e.g., `TestClassifyName`). Avoid creating multiple separate single-case test functions for similar logic. Table-driven tests are preferred for unit tests with multiple input/output scenarios. Integration tests that require complex setup may use procedural sub-tests.
- **Use Test Helpers**: Extract repetitive test setups into helper functions and use `t.Helper()` to ensure test failure line numbers remain accurate.

## 2. Documentation and Comments

- **Concise Language**: Code comments must be minimal, concise, and straightforward. Get straight to the point.
- **Technical Jargon**: Use relevant common technical jargon to minimize word count. Do not explain standard computer science terms (e.g., "hash function").
- **References**: Add references or documentation links for uncommon terms or specific algorithms (e.g., linking to BLAKE3 documentation).
- **Public vs. Private Functions**:
  - **Public functions** require a docstring explaining what the function does, its parameters, and its return value.
  - **Private functions** need a simple comment above the definition explaining their purpose. Private functions with self-documenting names and straightforward logic (< 5 lines) may omit comments.
  - **Constructors**: A `New()` function (or `NewStruct()`) that acts as a constructor does not need a docstring if its purpose is obvious from context. Constructors are exempt only if they take zero parameters or all parameters have self-documenting names and types.

## 3. Naming Conventions

- **English-like Semantics**: Package and function names should combine to read like an English phrase. For example, `location.Resolver()` is preferred as it clearly states "location resolver".
- **Avoid Abbreviations**: Do not use abbreviations or acronyms in function or struct names unless they are widely accepted industry standards. For example, prefer `hashFile()` over `hf()`.
- **Descriptive Variables**: Variables and constants must have descriptive names that clarify their intent. Single-letter variables are only acceptable for common loop indices (`i`, `j`, `k`) or standard conventions (`err` for errors, `ctx` for context).
- **Generic Function Names**: Keep function names generic enough to allow underlying implementations to change. For instance, use `hashFile()` instead of `BLAKE3Hash()`.

## 4. Code Structure & Best Practices

- **No Magic Values**: Avoid hardcoded numbers or string literals in the logic. Define them as constants with descriptive names and docstrings explaining *why* the value was chosen (e.g., `const DefaultTimeout = 500 * time.Millisecond`). This applies to configuration values, thresholds, status strings, and environment variable names. It does not apply to error message strings, SQL DDL in migrations, or standard format specifiers.
- **Error Wrapping**: Always wrap errors with context using `fmt.Errorf("description: %w", err)` to preserve the original error trace and add debugging context.
- **Context Propagation**: Always pass `context.Context` as the first argument to any function performing blocking I/O, database operations, or long-running tasks.
- **Bounded Concurrency**: Use bounded worker pools rather than unbounded goroutines to prevent resource exhaustion (e.g., spinning up a fixed number of workers based on `workerCount`). Avoid fire-and-forget goroutines that can lead to unbounded resource usage.
- **Dependency Injection**: Pass dependencies (like loggers, database connections, and path resolvers) through constructors rather than relying on global state or singletons.
- **Logging**: Use the application's custom logging library (e.g., `h.log.Info()`) instead of the standard library `log` package (e.g., avoid `log.Printf()`).
- **Database & Concurrency**: Avoid `SELECT`-then-`INSERT` patterns that can cause race conditions. Instead, rely on atomic operations like `INSERT ... ON CONFLICT DO UPDATE` (upserts).
