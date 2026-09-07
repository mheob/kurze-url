# Restoring the database

The production database is backed up nightly to a **private** repository, `kurze-url-backups`, as an encrypted release asset. This procedure turns one of those assets back into a running instance.

It lives here, in the public repository, on purpose: a recovery procedure stored only inside the system you are recovering from is unreachable exactly when you need it. Nothing below is secret. The secrets are the database URL and the `age` private key, and both live elsewhere.

## What you need

- The `age` **private** key. Without it every backup is unreadable, and no backup contains it. You stored two copies in separate places.
- `age`, `tar`, the `supabase` CLI, `psql`, and `gh`.
- Access to the Supabase account and both Vercel projects.

## What a restore does not bring back

Sessions. Password hashes travel with the dump, so everyone can log in again — but everyone is logged out, because a fresh project signs tokens with new keys.

## Procedure

### 1. Fetch the newest backup

```bash
gh release list --repo mheob/kurze-url-backups --limit 5
gh release download backup-YYYY-MM-DD --repo mheob/kurze-url-backups
```

### 2. Decrypt and unpack

```bash
age -d -i /path/to/backups-key.txt -o backup.tar.gz kurze-url-YYYY-MM-DD.tar.gz.age
mkdir -p restore && tar xzf backup.tar.gz -C restore
ls -l restore   # roles.sql, schema.sql, data.sql
```

### 3. Create a fresh Supabase project

Frankfurt/EU region — the same choice `docs/planning/08-legal-and-compliance.md` argues for, and not one to re-decide under pressure.

Note its project ref, its database password, and its connection strings.

### 4. Apply the three files, in this order

Set `$NEW_DATABASE_URL` to the new project's **session pooler** connection string, port 5432 — not the direct connection, which is IPv6-only and not every network can reach it, and not the transaction pooler, which cannot do the session-level work `CREATE ROLE` and schema DDL need.

```bash
export NEW_DATABASE_URL="<new project's session pooler connection string, port 5432>"

psql "$NEW_DATABASE_URL" --single-transaction --variable ON_ERROR_STOP=1 -f restore/roles.sql
psql "$NEW_DATABASE_URL" --single-transaction --variable ON_ERROR_STOP=1 -f restore/schema.sql
psql "$NEW_DATABASE_URL" --single-transaction --variable ON_ERROR_STOP=1 -f restore/data.sql
```

Order is not cosmetic: roles are referenced by the schema, and the schema is referenced by the data. `ON_ERROR_STOP=1` matters as much — without it `psql` reports success after skipping every statement that failed.

The schema file deliberately does not contain the `auth` schema; the new project brought its own. The data file deliberately does contain `auth` data, which is how the users come back.

### 5. Repair the migration history

```bash
supabase link --project-ref <new-ref>
supabase migration list
```

The dump does not carry `supabase_migrations`, so the restored project has a complete schema and an empty history. Left alone, the Supabase GitHub integration would try to apply every migration again on the next merge to `main` and damage what you just recovered.

Mark every migration already present as applied:

```bash
supabase migration repair --status applied <version>
```

Repeat for each version `supabase migration list` shows as local-only, then run `supabase migration list` again and confirm both columns match.

### 6. Reconfigure the new Supabase project

Two things live in project settings rather than in Postgres, and both fail quietly:

- **Resend as custom SMTP.** Without it Supabase's built-in sender caps at two mails an hour, and team invitations silently stop arriving.
- **Auth redirect URLs and the site URL**, pointing at `https://www.kurze-url.app`.

### 7. Repoint both Vercel projects

Six variables. Missing any of them leaves a working database nobody can use.

| Project | Variable | New value |
| --- | --- | --- |
| `kurze-url-api` | `DATABASE_URL` | the new project's transaction pooler URL, port 6543 |
| `kurze-url-api` | `SUPABASE_JWKS_URL` | `https://<new-ref>.supabase.co/auth/v1/.well-known/jwks.json` |
| `kurze-url-api` | `SUPABASE_JWT_ISSUER` | `https://<new-ref>.supabase.co/auth/v1` |
| `kurze-url-api` | `SUPABASE_SERVICE_ROLE_KEY` | the new project's service-role key |
| `kurze-url-web` | `SUPABASE_URL` | `https://<new-ref>.supabase.co` |
| `kurze-url-web` | `SUPABASE_PUBLISHABLE_KEY` | the new project's publishable key |

Redeploy both projects afterwards; environment variables are read at runtime, but the deployment has to be replaced for them to take effect.

### 8. Confirm the restore actually worked

Not "the data is there" — that is the easy half:

1. `curl -sI https://go.kurze-url.app/<a known slug>` returns a 301 or 302 to the right destination.
2. Log in at `https://www.kurze-url.app` **as an account that existed before the backup**, with its existing password or magic link.
3. That account sees its teams and its links.

Step 2 is the whole point. A restore that stops at step 1 has proven only that Postgres accepted the file.

### 9. Point the backup workflow at the new database

Update `DATABASE_URL` in the `kurze-url-backups` repository's secrets to the new project's **session pooler** string, and run the workflow by hand once. A restored instance that is no longer being backed up is one incident from the same position again.

## Drill

Repeat this procedure annually, and whenever Supabase changes anything about auth or the dump format. `scripts/restore-local.sh` does steps 2 and 4 against a local `supabase start`, which is the cheap half and catches most breakage; the hosted run is what proves step 8.

`supabase start` already ran this repository's migrations, so the local database is not empty the way a fresh project is. Before applying anything, the script resets it: it drops and recreates the `public` schema, and clears the `auth` tables the dump repopulates — otherwise it would be applying the dump on top of an already-seeded database, which is a merge, not a restore, and fails on constraints that already exist rather than testing whether the backup is any good. Because that reset is destructive, the script talks only to the hardcoded local connection (`127.0.0.1:54322`), refuses to run if that connection isn't exactly what it expects, and refuses any argument that looks like a connection string rather than a directory. It also prints a warning before touching anything, in case it's run in the wrong terminal.
