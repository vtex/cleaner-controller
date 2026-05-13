# Specification: AI Agent Setup — cleaner-controller

**Ticket:** CLOUD-608

## 1. Implementation Decisions

- **Proposed Solution:** Bootstrap harness-agnostic AI agent support for the cleaner-controller repo: root `AGENTS.md` (runbook), `CLAUDE.md` (constitution), `.agents/` scaffold, `.github/copilot-instructions.md`, and `.specify/` directory structure. Remove Spec Kit auto-generated scaffolding and gitignore it to prevent drift.
- **Risks & Mitigations:** Constitution file (`CLAUDE.md` / `.specify/memory/constitution.md`) can become stale as the codebase evolves — mitigated by the Governance section requiring PR review for amendments and a quarterly maintenance cadence documented in the agents README.
- **Execution Plan:**
  1. Add harness-agnostic scaffolding (`.agents/`, `.specify/`, `AGENTS.md`, `CLAUDE.md`)
  2. Apply go-service constitution template to `.specify/memory/constitution.md`
  3. Remove auto-generated Spec Kit files and add `.gitignore` entries
  4. Rewrite `AGENTS.md` to follow the sdd-templates root template (< 80 lines, high-signal)

## 2. Technical Contract

### 2.1 Files Delivered

| File | Purpose |
|---|---|
| `AGENTS.md` | Root agent runbook — commands, architecture boundaries, reconciler facts, guardrails |
| `CLAUDE.md` | Claude Code context file (mirrors constitution pointer) |
| `.specify/memory/constitution.md` | Non-negotiable engineering principles (Go service constitution v1.1.0) |
| `.github/copilot-instructions.md` | Copilot-specific agent instructions |
| `.agents/commands/.gitkeep` | Placeholder for future agent command files |
| `.agents/rules/.gitkeep` | Placeholder for future agent rule files |
| `.agents/skills/.gitkeep` | Placeholder for future agent skill files |

### 2.2 Gitignore Entries Added

```
.agents/skills/speckit-*/
.github/agents/speckit*
.github/prompts/speckit*
```

### 2.3 Invariants & Constraints

- `AGENTS.md` must stay under 80 lines; every line must prevent at least one wasted agent tool call.
- `AGENTS.md` and `.specify/memory/constitution.md` must not duplicate each other — `AGENTS.md` covers commands and navigation facts; the constitution covers principles and governance.
- Spec Kit scaffolding (speckit-* files) must never be committed — covered by `.gitignore`.
- Constitution amendments require PR review by a CODEOWNERS maintainer and a version bump.
