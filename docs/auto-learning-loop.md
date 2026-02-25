# Auto-learning Loop & SOUL/AGENT Tuning (Draft)

Version: 0.1 (2026-02-24)
Owner: Diego / Chango

Problem
- The agent needs a continuous learning loop to refine behavior (SOUL.md) and operating instructions (AGENT.md) from real interactions and outcomes.

Goals
- Capture stable preferences and constraints automatically.
- Summarize periodic learnings into MEMORY.md and Daily Notes.
- Propose edits to SOUL.md and AGENT.md as draft PRs, not auto-merge.

Data Sources
- memory/MEMORY.md, Daily Notes (workspace/memory/YYYYMM/...), task outcomes, reminders, and recent conversations (last 7 days).

Loop (default cadence)
- Daily micro-pass (end of day): extract 3–5 nuggets → append to Daily Notes.
- Weekly synthesis (Sun): propose consolidated updates to MEMORY.md + optional SOUL/AGENT suggestions.

Design
- A background cron task triggers a summarize skill that:
  1) Gathers last 24h/7d artifacts.
  2) Produces concise learnings (bullets) + candidate updates.
  3) Opens/updates a draft PR with changes to MEMORY.md and a suggestions file: .
- Always human-in-the-loop: never auto-merge.

Implementation Phases
- Phase 1 (this PR):
  - Spec + scaffolding (this doc, PR template checklists).
  - Add  placeholder.
- Phase 2:
  - Implement summarize skill wrapper + cron entry.
  - Write to Daily Notes and open/update PR with changes.
- Phase 3:
  - Diff-aware edits to SOUL.md/AGENT.md behind feature flag.

Acceptance Criteria (Phase 1)
- This spec exists and is tracked.
- PR template includes Auto-learning checklist.
-  placeholder committed.

Fail-safes
- Feature flag: AUTO_LEARNING_WRITE=false by default.
- Draft PRs only, explicit reviewer required.

Open Questions
- Thresholds for what becomes “stable” memory entries.
- Cadence defaults for different workloads.