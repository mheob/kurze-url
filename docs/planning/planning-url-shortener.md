# URL Shortener Planning

Not all features need to be implemented right away. We’ll determine in the detailed planning phase which features are absolutely essential for the MVP. All other features should at least be considered so we can make preparations if necessary.

## Technology

- Backend: GOLANG
- Frontend: “Tanstack Start” app
- Database: Supabase (Free Tier)
- CLI: GOLANG
- possibly Redis: Upstash Redis (Free Tier)

## Features

### Dev

- Self-Hosted
- API-first
- Custom Slugs
  - `go.example.de/sommerfest`
  - optional automatically generated IDs

- Configurable length and character set
  - Validation (no special characters, hyphen separator)
- CLI
  - ```bash
    short link create https://example.com \
      --slug summer-party \
      --tag club
    short link list
    short link stats summer-party

    short qr summer-party
    ```
- Nice-to-have:
  - Browser extension + share sheet
  - Redirect performance via Redis

### Core

- Comprehensive analytics
  - Total clicks
  - Unique visitors, to the extent that they can be measured in a privacy-friendly manner
  - Time series
  - Browser
  - Operating system
  - Device type

- Country
  - Referrer
  - UTM parameters
  - Bots vs. humans
  - QR vs. regular link
- Expiration date
- Tags / Folders / Projects
- Search and filter by URL, alias, creator, and time period
- QR codes
  - Automatic generation
  - SVG + PNG download

- Configurable size
  - Error correction level
  - Margin / Quiet zone
  - Optional logo in the center
  - Configurable colors
  - QR code always points to the short URL, not directly to the destination
- Change destination URL later
  - Without invalidating QR codes or printed links
- Security & Privacy by Default
  - HTTPS + HSTS for all links
  - Malware/phishing scanning of destination URLs before publication
  - Referrer handling (strip or replace instead of pass-through)
  - Optional password protection, expiration dates, and geotargeting

### Advanced

- Users, Teams, and Permissions
  - Role-based permissions (Owner, Admin, Editor, Viewer)
  - Permanent logging of “Creator” and “Change History,” including timestamps
  - OAuth/OIDC, passkeys/WebAuthn, MFA
- Bulk creation
- Import/export via Excel, CSV, or JSON
- Configurable query parameters
  - Accept query
  - Reject query
  - Allow individual parameters
  - Override parameters
  - Add fixed parameters
- Preview page
  - Example `go.example.de/abcd+`
  - Link leads to `https://www.example.com/...` including

- Target domain
  - Link creator, if public
  - Creation date
  - Optional security status

### Privacy

Make sure the shortener does not simply pass through the referrer header—only 2 out of 8 tested services strip it completely. Important for GDPR compliance: IP anonymization, data residency (EU servers), export/delete functions.

- Privacy by Default —> GDPR-compliant
  - no cookies
  - no fingerprinting
  - full IP addresses never stored
  - configurable retention
  - GeoIP processed locally
  - analytics can be disabled
  - automatic deletion after 90 days

- Server-side tracking

## Security and Abuse Prevention

- URL scheme allowlist:
  - `https://`
- Block `javascript:`, `data:`, `file:`, etc.
- Protection against SSRF
- Account for DNS rebinding
- Block localhost/private IPs if URLs are accessed on the server side
- Rate limits
- Captcha optional for public instances
- Link reporting feature
- Domain blocklist
- Ability to disable links
- Audit log
- Link health monitoring (broken link checker)
  - Dashboard display: Healthy, Redirected, Broken, Unknown
- Optional Safe Browsing/Threat Intelligence integration
