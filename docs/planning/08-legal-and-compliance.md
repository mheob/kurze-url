# URL Shortener — Legal & Compliance Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code — and, unlike the other planning docs, this one should also be reviewed by an actual lawyer before the shared instance goes live. Everything below is a reasonable-fit engineering assessment, not legal advice, in the same spirit as the Safe Browsing licensing note and the BFSG accessibility note already flagged in `02-external-services-and-hosting.md` and `03-frontend.md`.

Project context: one shared multi-tenant instance (see `01-architecture.md`) operated by the maintainer(s) on behalf of multiple German non-profit associations ("Vereine"). That shape — one operator, multiple organizational customers, German/EU audience — is what drives every decision below.

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| Impressum | The maintainer(s), as operator of the shared instance, publish one Impressum for the app itself | Required under DDG §5 (successor to TMG §5 — substantively unchanged); doesn't replace each Verein's own existing Impressum obligation for their own separate website |
| Datenschutzerklärung | The maintainer(s) publish one privacy policy covering the platform's own data processing | Must name every third-party processor in the stack — see below |
| Processor relationship | Standard AVV (Auftragsverarbeitungsvertrag / DPA), accepted by each Verein when they create a team | Maintainer(s) act as Auftragsverarbeiter for Verein-controlled data; one specific nuance flagged, not resolved, for click analytics — see below |
| Cloud provider region | **Frankfurt/EU region**, selected wherever the provider offers it (confirmed available on Supabase's free tier) | Addresses data _residency_, not full data _sovereignty_ — see "Data processor locations" below |

## Impressum

Decided: the shared instance itself (the app the Vereine log into — marketing/login pages, dashboard) needs its own Impressum, published by whoever operates it. This is unchanged in substance from the old TMG §5 requirement under the renamed Digitale-Dienste-Gesetz (DDG) — name/address (no PO box), a working contact method, reachable within one or two clicks from anywhere on the site.

This is a **separate** obligation from each Verein's own Impressum on their own existing website — that's each Verein's problem already, not something this project introduces or solves.

**A specific edge case worth flagging, arising directly from the redirect/password design in `06-api-design.md`**: the password-verification interstitial page (`GET/POST /{slug}/verify`) is a real page a visitor sees and interacts with, served on a team's own custom domain — arguably a "Telemedium" in its own right for the moment someone is looking at it, not just a transparent redirect. Whether that specific page needs its own Impressum link (and whose — the maintainer's, since they built and operate the page, even though it's rendered under the Verein's domain) isn't resolved here; flagging it as a concrete question for the eventual legal review rather than guessing at an answer that could be wrong in either direction.

## Datenschutzerklärung (privacy policy)

Decided: the maintainer(s) publish one privacy policy for the platform, covering:

- Account/auth data (Supabase-managed `auth.users`, team membership) — per `05-database-schema.md`.
- Click analytics — the hashed-IP+UA unique-visitor counting, GeoIP, 90-day retention, already designed privacy-first in `01-architecture.md`; the policy documents what's _already_ the minimal-collection design, rather than that design needing to change for this doc.
- Every third-party processor in the stack, since each one is a place data actually flows to: Supabase, Upstash, Vercel, Google Safe Browsing, Resend (per `02-external-services-and-hosting.md`).
- A note on the redirect flow itself: destination URLs entered by users are sent to Google Safe Browsing for scanning (already decided, `01-architecture.md`) — worth being explicit that this is the _destination_ URL only, not visitor data.

## Processor relationships & the AVV

Decided: a standard AVV/DPA, presented to and accepted by each Verein at team-creation time (a natural fit for the already-designed `POST /v1/teams` flow in `06-api-design.md` — an acceptance checkbox referencing a versioned document, not a novel piece of infrastructure to build).

For most of the data in this system, the relationship is the standard SaaS shape: the Verein is **Verantwortlicher** (controller) — they decide to use the tool, create links, add members — and the maintainer(s) are **Auftragsverarbeiter** (processor), acting only on the Verein's instructions per Art. 28 DSGVO.

**Nuance flagged, not resolved**: for the _anonymous end-visitor click analytics_ specifically (someone clicking a short link, who is not a Vereinsmitglied and never interacts with the app directly), the "who decides purposes and means" question is less clean. The Verein didn't choose _what_ gets tracked or how long it's retained — the platform's own product design did (the hashed-IP+UA scheme, the 90-day window, the dimension set). That's a real argument for the maintainer being at least a joint controller for that one specific processing activity, not a pure processor acting on Verein instructions. This matters in practice for who's responsible for responding to a data-subject request about click data, and isn't something to guess at here — a concrete item to put in front of the eventual legal review, alongside the interstitial-page Impressum question above.

## Data processor locations (US CLOUD Act exposure)

Both Supabase and Upstash are US (Delaware) companies. Choosing their Frankfurt/`eu-central-1` region — confirmed available on Supabase's free tier — keeps the actual data physically in the EU (data _residency_), but doesn't remove the companies' own exposure to the US CLOUD Act as US legal entities (data _sovereignty_ is a different, unsolved property). This is the same shape of trade-off as the Safe Browsing licensing decision already made: a real, non-zero residual risk, accepted rather than eliminated, because the alternative (EU-native providers, or self-hosting) was already deliberately ruled out elsewhere for cost and developer-experience reasons (`02-external-services-and-hosting.md`, `04-backend-architecture.md`).

Practical, zero-cost action to take now rather than revisit later: **select the Frankfurt/EU region explicitly** when creating the Supabase project (and Upstash database, if it offers the same choice) — this is a one-time setup decision worth getting right from the start rather than migrating a live database's region later. All of these providers publish a standard DPA/SCCs for EU customers as a matter of course (this is a well-trodden path for any EU business using US-headquartered SaaS), which covers the legal mechanism for the transfer even though it doesn't remove the CLOUD Act point above.

## Cookies / consent banner

Worth a brief, deliberately narrow note rather than a full section: `01-architecture.md` already decided "no cookies, no fingerprinting beyond the [analytics] hash" for the _analytics_ side, which needs no consent banner regardless (it isn't cookie-based to begin with). Whatever mechanism ends up holding the _auth session_ (a cookie vs. browser storage — not yet pinned down at the implementation level, since Supabase's OAuth 2.1 Server flow supports either) matters here: a strictly-necessary session cookie is exempt from consent under TTDSG §25(2) regardless, so no banner is needed purely for login state either — but this is worth reconfirming once the actual session-storage mechanism is implemented, not assumed permanently settled from a planning doc.

## Not yet decided / to revisit

- Actual legal text for the Impressum, Datenschutzerklärung, and AVV — this doc identifies what's needed and why, not the text itself, which needs a lawyer familiar with German/EU non-profit and data-protection law before the shared instance is opened to real Vereine.
- Whether the password-interstitial page needs its own Impressum reference (flagged above).
- Who is the responsible party (controller vs. joint controller) for click-analytics data-subject requests (flagged above).
- Whether Upstash's free tier offers the same EU-region selection Supabase's does — not yet confirmed, worth checking at setup time.
