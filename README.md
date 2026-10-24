# URL Shortener

A multi-tenant URL shortener for German non-profit associations ("Vereine"), run as one shared, open-source instance.

> **Status:** early development. The API foundation and the redirect hot path (`GET /<slug>`, password-protected links, rate limiting, analytics rollups) exist; the rest of the design is documented in `docs/planning/`.

## What it does

- Short links on shared or custom domains, with folders and tags
- Optional password protection and QR codes for every link
- Privacy-friendly click analytics — daily aggregates only, no raw click log and no stored IP addresses
- Teams with roles, invitations, and an audit log
- English and German user interface

## Command-line tool

`kurze-url` creates short links, lists them and shows their numbers from the terminal. It is a thin client over the same HTTP API as the web app, so it acts with your own rights in your own teams, and it speaks English or German by your system locale.

### Install

**macOS and Linux** (amd64 and arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.sh | sh
```

The script downloads the latest release's archive for your machine and its `checksums.txt`, checks the archive against it, and refuses to install on a mismatch. It puts `kurze-url` in `~/.local/bin`. When that directory is not on your `PATH` it prints the line to add, for `~/.zshrc`, `~/.bashrc` or `~/.profile` depending on your `$SHELL`. Bash on macOS reads `~/.bash_profile` for login shells instead, and fish users run `fish_add_path ~/.local/bin`. It does not run in Git Bash or Cygwin; it says so and points to the PowerShell installer below.

Two environment variables change where it installs and what. They go on the `sh` side of the pipe, where the script reads them:

```sh
curl -fsSL https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.sh | KURZE_URL_INSTALL_DIR="$HOME/bin" KURZE_URL_VERSION=v0.1.0 sh
```

- `KURZE_URL_INSTALL_DIR`: the directory for the binary (default `~/.local/bin`).
- `KURZE_URL_VERSION`: a release tag such as `v0.1.0` (default: the latest release).

**Windows** (amd64 and arm64), two ways to choose from:

- **PowerShell:** run this in Windows PowerShell 5.1 or PowerShell 7.

  ```powershell
  irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex
  ```

  It does the same as `install.sh` with the Windows archive, `kurze-url_windows_<arch>.zip`: it checks the checksum before it unpacks anything, and installs `kurze-url.exe` into `%LOCALAPPDATA%\Programs\kurze-url`, or into `$env:KURZE_URL_INSTALL_DIR`. It then **adds that directory to your user `Path`**, for every new terminal and for the one you ran it in. Set `$env:KURZE_URL_NO_MODIFY_PATH = '1'` first to leave your `Path` alone; the script then reminds you to add the directory yourself. `$env:KURZE_URL_VERSION = 'v0.1.0'` picks a release the same way as above.

- **By hand:** download `kurze-url_windows_<arch>.zip` (`amd64` or `arm64`) and `checksums.txt` from the [releases page](https://github.com/mheob/kurze-url/releases), compare the archive's SHA-256 with the line in `checksums.txt`, and put `kurze-url.exe` in a directory that is on your `Path`.

**Downloading the archive yourself on macOS** (`kurze-url_darwin_<arch>.tar.gz` from the releases page) works too, but a browser marks what it downloads as quarantined, and the binary, which is not notarised, needs the mark removed before it runs: `xattr -d com.apple.quarantine kurze-url`. The install script avoids this, because `curl` does not set the mark.

`go install` is not offered. The CLI is a nested Go module, so `go install` would need `apps/cli/vX.Y.Z` tags of its own, and a binary built without the release's linker flags prints `dev` as its version.

`kurze-url version` shows what you have, for example `kurze-url 0.1.0 (d764e8a, 2026-10-24T06:49:29Z)`.

### First steps

```sh
kurze-url login
kurze-url link create https://example.org/sommerfest --slug sommerfest
kurze-url link list
kurze-url link stats sommerfest
kurze-url link qr sommerfest
```

- `login` opens your browser on the web app's consent page and waits for the answer on `127.0.0.1`, on the first free port of 53682 to 53684. It prints the address when it cannot open a browser. With several teams, on a terminal, it asks which one is your default.
- `link create` prints the short URL and nothing else, so `| pbcopy` works. `--slug`, `--folder`, `--domain` and `--team` are optional.
- `link list` shows your team's newest links (20, or `--limit` up to 100).
- `link stats` shows clicks and unique visitors for the last 7 and 30 days, first the human ones and then everything including bots. Visitors are counted per day and added up, so someone who comes back on three days counts three times.
- `link qr` writes `<slug>.svg` (or `.png` with `--format png`) to the current directory. The code holds the short URL, never the destination.
- `logout` ends only the CLI's own session. Signing out of the web app leaves the CLI signed in, unless you tick "Sign out everywhere" there.

`kurze-url --help` and `kurze-url <command> --help` list every option. They are in English; everything a command prints is translated.

### Config

Settings live in a YAML file in your user config directory, which is where Go's `os.UserConfigDir()` points:

| System  | File                                                  |
| ------- | ----------------------------------------------------- |
| macOS   | `~/Library/Application Support/kurze-url/config.yaml` |
| Linux   | `~/.config/kurze-url/config.yaml`                     |
| Windows | `%AppData%\kurze-url\config.yaml`                     |

`KURZE_URL_CONFIG` names another file. Defaults can be global or belong to one team, and a flag beats the team's value, which beats the global one:

```yaml
language: de
team: sv-gruenwald

qr:
  format: png
  size: 1024

teams:
  sv-gruenwald:
    folder: newsletter
    domain: links.sv-gruenwald.de
    qr:
      fg: '1a3a6e'
      bg: 'ffffff'
```

Change it by hand or with `kurze-url config set`, which keeps your comments and the order of the file:

```sh
kurze-url config set language de
kurze-url config set folder newsletter --team sv-gruenwald
kurze-url config get
```

The keys are `language`, `team`, `folder` and `domain` (the last two per team, with `--team`), and `qr.format`, `qr.size`, `qr.fg` and `qr.bg`. Your sign-in is not in this file: it lives in the system keychain, or in a file only you can read where there is no keychain.

### Releasing (maintainers)

A release is a tag, and the tag is the maintainer's decision: nothing in this repository creates one. `.github/workflows/release-cli.yml` builds the tag with goreleaser and publishes six archives and `checksums.txt` to a GitHub Release. Their names carry no version (`kurze-url_<os>_<arch>.tar.gz`, `.zip` for Windows) because both install scripts download from `releases/latest/download`.

1. Tag a commit on `main` as `vX.Y.Z-rc.N`, for example `v0.1.0-rc.1`, and push the tag. It becomes a GitHub pre-release, which "latest" skips, so both install scripts keep installing the last final version while you check this one. Install the candidate with `KURZE_URL_VERSION=v0.1.0-rc.1`.
2. Run the manual checklist from [the CLI spec](docs/superpowers/specs/2026-10-23-cli-design.md) ("Testing") against the pre-release on macOS, Linux and Windows.
3. Tag the commit you checked as `vX.Y.Z` and push it. That is the release: it builds the final version, and "latest" moves to it.

A tag whose commit is not on `main` is refused. The release notes list only commits scoped `(cli)`.

## Planned stack

| Layer    | Choice                                    |
| -------- | ----------------------------------------- |
| Backend  | Go (chi + Huma), sqlc for database access |
| Database | Supabase (Postgres), EU region            |
| Cache    | Upstash Redis                             |
| Frontend | TanStack Start (React) with shadcn/ui     |
| CLI      | Go, over the same HTTP API                |
| Hosting  | Vercel                                    |

## Repository layout

```
apps/
  api/            # Go backend
  web/            # TanStack Start frontend
  cli/            # Go CLI
packages/
  api-client/     # TypeScript client generated from the OpenAPI spec
supabase/         # Database migrations
docs/planning/    # Architecture and design documents
```

## Documentation

Start at [`docs/planning/00-index.md`](docs/planning/00-index.md) for a map of the design documents and the decision log. [`CLAUDE.md`](CLAUDE.md) holds the condensed conventions that apply across the whole repository.

## License

MIT

## Running the stack locally

Prerequisites: Go 1.27+, Node 24+, Docker, the [Supabase CLI](https://supabase.com/docs/guides/cli), and [sqlc](https://sqlc.dev).

```bash
# 1. Configure the two env files
cp apps/api/.env.example apps/api/.env        # then set VISITOR_SALT
cp apps/web/.env.example apps/web/.env.local  # then set the Supabase values below

# 2. Generate the local JWT signing key (see "Why a signing key" below)
printf '[]' > supabase/signing_keys.json
supabase gen signing-key --algorithm ES256 --append

# 3. Start everything
pnpm dev
```

`pnpm dev` runs `scripts/dev.ts`: it checks the prerequisites, starts Supabase and a Redis container if they are not already up, waits for both to answer, starts the Go API with `apps/api/.env` exported into its environment, waits for `/health`, and then starts the web app. Ctrl-C stops the API and the web app; Supabase and Redis stay up so the next run starts in seconds and the local database survives. `pnpm dev:stop` shuts those two down, and `pnpm db:reset` re-applies the migrations and the seed — which drops everything else in the local database, so it is deliberately not part of `pnpm dev`.

The API reads `os.Getenv` and has no dotenv dependency of its own. `pnpm dev` is what exports the file for it; running the binary by hand needs `cd apps/api && set -a && . ./.env && set +a && go run ./cmd/api`.

### Pointing both halves at the local Supabase

`supabase status` prints the values. In `apps/web/.env.local`:

```
SUPABASE_URL=http://127.0.0.1:54321
SUPABASE_PUBLISHABLE_KEY=<the PUBLISHABLE_KEY from `supabase status`>
```

And in `apps/api/.env`:

```
SUPABASE_JWKS_URL=http://127.0.0.1:54321/auth/v1/.well-known/jwks.json
SUPABASE_JWT_ISSUER=http://127.0.0.1:54321/auth/v1
```

Both halves must name the same Supabase instance. They do not have to — the web app signs a visitor in against whatever `SUPABASE_URL` says while the API verifies tokens against `SUPABASE_JWKS_URL` — and when they disagree, login succeeds and every `/v1` call answers 401. Worse, `team_member.user_id` is a foreign key into `auth.users`, so a user id minted by a hosted project cannot be written into a local database at all: creating a team fails on the constraint. `pnpm dev` refuses to start when the two origins differ.

### Why a signing key

The API verifies ES256 through JWKS and rejects the legacy HS256 shared secret. A local stack signs HS256 until `signing_keys_path` is set in `supabase/config.toml` (it is, in this repository) **and** the file it names exists — so without the key, sign-in works and every authenticated request is refused.

Generate it with the two commands above, never with a plain redirect: `supabase gen signing-key … > supabase/signing_keys.json` truncates the file before the CLI reads it, and the CLI reads the configured path before it generates anything, so that form always fails. The file holds private key material and is gitignored.

### Signing in

The seed creates `dev@example.test` and gives it the `dev-verein` team. Sign in with that address at http://localhost:3000/login; the magic link arrives in the local mail catcher at http://127.0.0.1:54324. No other address works — `signInWithOtp` runs with `shouldCreateUser: false`, so an unknown address never becomes an account.

The seed also creates a verified `short.test` domain with a `hello` link, so:

```bash
curl -i -H 'Host: short.test' http://localhost:8080/hello   # 302 to example.org
curl -i http://localhost:8080/v1/health                     # 200, API surface
```

Regenerate the database layer after changing a migration or a query:

```bash
cd apps/api && sqlc generate
```

### Teams and invitations

`POST /v1/teams` is restricted to the maintainer allowlist. Locally that is the seeded user, whose id is a fixed literal in `supabase/seed.sql` — in `apps/api/.env`:

```
MAINTAINER_USER_IDS=00000000-0000-0000-0000-0000000000a1
```

Only needed to create _further_ teams; the seed already gives that user `dev-verein`. For any other user, read the id out of the database the stack is actually using:

```bash
psql "$DATABASE_URL" -c "select id, email from auth.users;"
```

Invitation emails need `SUPABASE_SERVICE_ROLE_KEY` (and `SUPABASE_AUTH_URL`, if the project's auth URL differs from `SUPABASE_JWT_ISSUER`). Without it the API still runs: adding an address that already has an account works, and an unknown address is refused with 503.
