# Test Topic

This is a test knowledge topic to verify the knowledge injection pipeline works correctly.

- The knowledge loader scans `workspace/knowledge/` for topic directories
- Each topic has META.json (metadata + keywords) and KNOWLEDGE.md (content)
- Keywords are matched against user messages (requires 2+ matches)
- Top 2 topics are injected into the system prompt within a 4000 char budget
