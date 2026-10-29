---
paths:
  - "**/*.{py,pyi}"
  - "**/pyproject.toml"
  - "**/uv.lock"
---

# Python

Runtime: current stable CPython in a `uv venv`; run with `uv run python`, never a bare interpreter, never `pip install` into the system interpreter.

| purpose | tool |
|---|---|
| deps and venv | `uv` |
| lint and format | `ruff check`, `ruff format` |
| static types | `pyright` |
| tests | `pytest -q` |

Structural limits (line length, complexity, argument counts, docstring convention) are encoded in `[tool.ruff]` in `pyproject.toml`, not restated in prose. Type hints on every public function. Never a bare `except:`. Prefer `pathlib`. Tests live in `tests/`, mirroring the package layout.

Supply chain: `pip-audit` before deploying; pin exact versions; install with hashes where the lockfile supports it.
