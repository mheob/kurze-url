#!/usr/bin/env bash
# Apply a decrypted backup to the local Supabase stack.
#
# This is the cheap, repeatable half of the restore drill: it proves the three
# files apply cleanly and the user rows arrive. It cannot prove anyone can log
# in to a hosted project — that needs the real procedure in docs/restore.md.
#
# `supabase start` already ran this repository's migrations, so the local
# database is not empty — it has every table, constraint and seed row a real
# restore target would not have. Applying a dump on top of that would be a
# merge, not a restore, and would fail on constraints and primary keys that
# already exist, for a reason that has nothing to do with whether the dump is
# any good. So this script resets the local database to empty first: it drops
# and recreates the `public` schema, and clears the `auth` tables the dump
# will repopulate. That reset is destructive, which is why the rest of this
# script exists only to make sure it can never be destructive to anything but
# the local stack — see the two checks below before LOCAL_DB is used.
#
# Usage: scripts/restore-local.sh <directory containing roles.sql schema.sql data.sql>
set -euo pipefail

DUMP_DIR="${1:?usage: restore-local.sh <dump-directory>}"

# Guard 1: the one argument this script takes is a directory path, never a
# connection string — reject anything that looks like one before it is used
# for anything, in case this is ever run by habit with $NEW_DATABASE_URL from
# docs/restore.md instead of a dump directory.
case "$DUMP_DIR" in
	*://*|*@*)
		echo "refusing: '$DUMP_DIR' looks like a connection string, not a directory" >&2
		echo "this script only ever talks to the hardcoded local database below" >&2
		exit 1
		;;
esac

LOCAL_DB="postgres://postgres:postgres@127.0.0.1:54322/postgres"

# Guard 2: LOCAL_DB above is a hardcoded literal — it is never taken from an
# argument, flag, or environment variable, so there is no input that reaches
# it. This assertion is a second, independent check against that same
# literal, so a future edit that changes LOCAL_DB by accident (or replaces it
# with something read from the environment) does not silently make the reset
# below run against anything but 127.0.0.1:54322. There is no flag to skip it.
case "$LOCAL_DB" in
	postgres://postgres:postgres@127.0.0.1:54322/postgres) ;;
	*)
		echo "refusing: LOCAL_DB is not the expected local connection ($LOCAL_DB)" >&2
		exit 1
		;;
esac

cat >&2 <<'EOF'
!! This will DROP the public schema and clear the auth tables in the local
!! Supabase database at 127.0.0.1:54322, then apply the dump on top.
!! It only ever touches that local database, never anything hosted.
!! Ctrl-C now if this is the wrong terminal.
EOF

for f in roles.sql schema.sql data.sql; do
	if [ ! -s "$DUMP_DIR/$f" ]; then
		echo "missing or empty: $DUMP_DIR/$f" >&2
		exit 1
	fi
done

if ! pg_isready -d "$LOCAL_DB" >/dev/null 2>&1; then
	echo "local Supabase is not running — start it with 'supabase start'" >&2
	exit 1
fi

echo "resetting local database to empty"
psql "$LOCAL_DB" --no-psqlrc --single-transaction --variable ON_ERROR_STOP=1 <<'SQL'
drop schema if exists public cascade;
create schema public;
grant usage on schema public to postgres, anon, authenticated, service_role;
truncate auth.users cascade;
SQL

# ON_ERROR_STOP is the point of this script. Without it psql reports success
# after skipping every statement that failed, which is how a restore gets
# declared working when it is not.
for f in roles.sql schema.sql data.sql; do
	echo "applying $f"
	psql "$LOCAL_DB" --single-transaction --variable ON_ERROR_STOP=1 -f "$DUMP_DIR/$f"
done

echo
echo "row counts after restore:"
psql "$LOCAL_DB" --no-psqlrc --tuples-only --command \
	"select 'auth.users', count(*) from auth.users
	 union all select 'team', count(*) from public.team
	 union all select 'link', count(*) from public.link;"
