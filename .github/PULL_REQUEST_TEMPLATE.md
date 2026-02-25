## Summary

- [ ] Scope is clear
- [ ] Backward compatibility considered

## Auto-learning Loop Checklist (if applicable)
- [ ] Does not auto-merge; human-in-the-loop
- [ ] Writes to Daily Notes only if flag enabled
- [ ] Suggestions in docs/soul-agent-suggestions.md
- [ ] MEMORY.md changes are minimal and stable

## Testing
- [ ] No runtime changes (docs-only) OR
- [ ] Feature flag tested: AUTO_LEARNING_WRITE

## Rollback
- Revert PR; no side effects if flag is false.