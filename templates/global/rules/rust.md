---
paths:
  - "**/*.rs"
  - "**/Cargo.toml"
  - "**/Cargo.lock"
---

# Rust

Runtime: latest stable via `rustup`.

| purpose | tool |
|---|---|
| build and deps | `cargo` |
| lint | `cargo clippy --all-targets --all-features -- -D warnings` |
| format | `cargo fmt` |
| test | `cargo test` |
| supply chain | `cargo deny check` |

Style: exhaustive `match` with no `_` arm, explicit fields over `..`, `let ... else` for early returns, the happy path unindented. Newtypes over primitives; enums for state machines, not boolean flags; `thiserror` in libraries, `anyhow` in applications; `tracing`, not `println!`.

Panic prevention and hygiene are enforced through `[lints.clippy]` in `Cargo.toml` (`unwrap_used`, `panic`, `todo`, `unimplemented`, `dbg_macro`, `print_stdout` and `print_stderr` at `deny`; `pedantic` at `warn`), not in prose. Tests: `cargo-mutants` to prove tests catch failures; `proptest` for parsers, serialisation and algorithms.
