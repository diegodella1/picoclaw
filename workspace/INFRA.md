# INFRA.md — Infrastructure Reference

Customize this file with your own infrastructure details so Chango knows how to interact with your services.

---

## Machine
- (your machine specs)
- LAN IP: (your IP)

---

## GitHub
- CLI: `gh` (authenticate with `gh auth login`)
- Common ops:
  - `gh repo create <name> --public/--private --clone`
  - `gh pr create`, `gh pr list`, `gh pr merge`
  - `gh issue list`, `gh issue create`

---

## Deployment Platform (Coolify, Vercel, etc.)

Configure your deployment platform here. Example with Coolify:

```bash
# Restart existing app
curl -X POST "http://localhost:8000/api/v1/applications/<APP_UUID>/restart" \
  -H "Authorization: Bearer $TOKEN" -H "Accept: application/json"
```

---

## Database

Configure your database access here. Example with Supabase:

```bash
# Query
sudo docker exec supabase-db psql -U supabase_admin -d postgres -c "SELECT ..."

# Apply migration
sudo docker exec -i supabase-db psql -U supabase_admin -d postgres < migration.sql
```

---

## Docker
- `docker stats --no-stream` to check container resource usage

---

## Projects Directory
- List your active projects here so Chango knows about them

---

## Important Notes
- Add platform-specific quirks and gotchas here
- Document workarounds for known issues
