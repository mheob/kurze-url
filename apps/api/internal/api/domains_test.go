package api_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

func TestClaimDomainReturnsTheRecordsToCreate(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[api.Domain](t, rec)
	require.Equal(t, "links.verein.test", body.Hostname)
	require.Equal(t, "pending", body.VerificationStatus)
	require.NotEmpty(t, body.VerificationToken)
	require.Equal(t, "_kurze-url-challenge.links.verein.test", body.Records.TXT.Name)
	require.Equal(t, body.VerificationToken, body.Records.TXT.Value)
	require.Equal(t, "links.verein.test", body.Records.CNAME.Name)
	require.Equal(t, f.deps.Config.DomainDNSTarget, body.Records.CNAME.Value,
		"the CNAME value is what a Verein pastes into their DNS zone; it must be the configured target")
}

// TestClaimDomainIsRateLimited pins allowDomainClaim the same way
// TestAddMemberIsRateLimited (members_test.go) pins its own limiter: delete
// the allowDomainClaim call in createDomain and this is the only thing that
// notices, since every other claim test only ever claims once per fixture.
func TestClaimDomainIsRateLimited(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.DomainClaimRateLimitPerHour = 1
	f.rebuildRouter()

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "first-claim.verein.test"})
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "second-claim.verein.test"})

	require.Equal(t, http.StatusTooManyRequests, second.Code, "body: %s", second.Body.String())
}

func TestClaimDomainIsRefusedBelowAdmin(t *testing.T) {
	// A domain is the namespace a team's links live in, not content.
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestClaimDomainRejectsInvalidHostnames covers all three of
// NormalizeHostname's failure modes at the HTTP layer, not just the apex
// case: each must produce a 422 whose body actually carries that error's own
// wording, since ErrReserved and ErrMalformed share the same 422 line as
// ErrApex and a wrong mapping would still pass a test that checked only the
// status code.
func TestClaimDomainRejectsInvalidHostnames(t *testing.T) {
	f := newTenancyFixture(t)

	// The fixture's own sharedHostname ("shared-<suffix>.test") has only two
	// labels, so it trips ErrApex before NormalizeHostname ever reaches the
	// reserved check — that would test the wrong branch. A three-label
	// hostname that still equals the instance's own SharedDomainHostname is
	// what actually exercises ErrReserved, so it is set here instead of relied
	// on from the fixture.
	f.deps.Config.SharedDomainHostname = "reserved.verein.test"
	f.rebuildRouter()

	for _, tc := range []struct {
		name     string
		hostname string
		wantErr  error
	}{
		{"apex", "verein.test", domainverify.ErrApex},
		{"reserved (the instance's own shared hostname)", "reserved.verein.test", domainverify.ErrReserved},
		{"malformed", "https://x.verein.test/p", domainverify.ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
				"/v1/teams/"+f.teamID.String()+"/domains",
				map[string]string{"hostname": tc.hostname})

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.wantErr.Error())
		})
	}
}

// TestClaimDomainRefusesASecondClaimByTheSameTeam pins Finding 7's fix:
// without it, a second claim by the same team on a hostname it already holds
// would insert a second row, and verifying the first would then have
// FailCompetingClaims flip that second row to 'failed' — read by the UI as
// "another team verified this hostname first", which would be false, since
// both rows belong to the very team asking.
func TestClaimDomainRefusesASecondClaimByTheSameTeam(t *testing.T) {
	f := newTenancyFixture(t)
	hostname := "already-claimed-" + uuid.NewString()[:8] + ".verein.test"
	claimDomain(t, f, hostname)

	second := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": hostname})

	require.Equal(t, http.StatusConflict, second.Code, "body: %s", second.Body.String())

	var count int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from domain where team_id = $1 and hostname = $2`,
		f.teamID, hostname).Scan(&count))
	require.Equal(t, 1, count, "the refused claim must not leave a second row behind")
}

// TestClaimDomainAllowsARenewedClaimAfterFailure covers the one status a
// repeat claim must still be allowed through: GetActiveDomainClaimForTeam
// excludes 'failed' rows on purpose, so a team whose claim lost a
// verification race — or was manually failed some other way — can still
// claim the hostname again rather than being locked out by its own dead row.
func TestClaimDomainAllowsARenewedClaimAfterFailure(t *testing.T) {
	f := newTenancyFixture(t)
	hostname := "renewed-claim-" + uuid.NewString()[:8] + ".verein.test"
	first := claimDomain(t, f, hostname)

	_, err := f.pool.Exec(t.Context(),
		`update domain set verification_status = 'failed' where id = $1`, first.ID)
	require.NoError(t, err)

	second := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": hostname})

	require.Equal(t, http.StatusCreated, second.Code, "body: %s", second.Body.String())
}

func TestTwoTeamsMayClaimTheSameHostname(t *testing.T) {
	// The whole point of the partial index: a claim is not a reservation.
	f := newTenancyFixture(t)

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "contested.verein.test"})
	require.Equal(t, http.StatusCreated, first.Code)

	second := f.do(t, f.otherAdmin, http.MethodPost,
		"/v1/teams/"+f.otherTeamID.String()+"/domains",
		map[string]string{"hostname": "contested.verein.test"})
	require.Equal(t, http.StatusCreated, second.Code, "body: %s", second.Body.String())

	require.NotEqual(t,
		decode[api.Domain](t, first).VerificationToken,
		decode[api.Domain](t, second).VerificationToken,
		"each claim needs its own token, or the first claimant could verify the second's")
}

func TestListDomainsHidesAnotherTeamsDomains(t *testing.T) {
	f := newTenancyFixture(t)
	claimDomainAs(t, f, f.otherAdmin, f.otherTeamID, "hidden.verein.test")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/domains", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	for _, item := range decode[api.Page[api.Domain]](t, rec).Items {
		require.Equal(t, f.teamID, item.TeamID)
	}
}

func TestGetDomainReturnsTheRecordsToCreate(t *testing.T) {
	f := newTenancyFixture(t)
	claimed := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/domains/"+claimed.ID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[api.Domain](t, rec)
	require.Equal(t, claimed.ID, body.ID)
	require.Equal(t, claimed.Hostname, body.Hostname)
	require.Equal(t, claimed.VerificationToken, body.VerificationToken,
		"the token must still be readable on a later GET, not only on the create response")
	require.Equal(t, "_kurze-url-challenge.links.verein.test", body.Records.TXT.Name)
	require.Equal(t, body.VerificationToken, body.Records.TXT.Value)
}

func TestGetDomainIs404ForANonMember(t *testing.T) {
	f := newTenancyFixture(t)
	claimed := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/domains/"+claimed.ID.String(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVerifyReportsWhichHalfIsMissing(t *testing.T) {
	f := newTenancyFixture(t)
	f.domainVerifier.reason = domainverify.ReasonTokenMissing

	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode[struct {
		Domain api.Domain `json:"domain"`
		Reason string     `json:"reason"`
	}](t, rec)
	require.Equal(t, "pending", body.Domain.VerificationStatus)
	require.Equal(t, "token_missing", body.Reason,
		"a Verein that cannot see which half failed cannot fix it")

	// The response above is built from the row loaded before any write could
	// happen, so it would still read "pending" even if the handler had wrongly
	// written "verified" and returned the stale value. Only a direct read of
	// the row proves nothing was written.
	var status string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select verification_status from domain where id = $1`, claim.ID).Scan(&status))
	require.Equal(t, "pending", status,
		"a failed check must not write verified to the row")
}

func TestVerifySucceedsAndSettlesCompetingClaims(t *testing.T) {
	f := newTenancyFixture(t)
	f.domainVerifier.reason = domainverify.ReasonNone

	// Suffixed, unlike the fixture's other literal hostnames: this is the
	// first test in the suite to actually reach "verified", the one status
	// the partial unique index makes globally exclusive. A hard-coded value
	// here would leave a verified row behind if the run were ever killed
	// mid-test, and every later run would then 409 on this same hostname.
	hostname := "contested-" + uuid.NewString()[:8] + ".verein.test"
	mine := claimDomain(t, f, hostname)
	theirs := claimDomainAs(t, f, f.otherAdmin, f.otherTeamID, hostname)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+mine.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "verified", decode[struct {
		Domain api.Domain `json:"domain"`
	}](t, rec).Domain.VerificationStatus)

	var status string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select verification_status from domain where id = $1`, theirs.ID).Scan(&status))
	require.Equal(t, "failed", status,
		"the losing claim must be answered, not left pending forever")
}

// TestVerifyRefusesASecondTeamsAlreadyVerifiedHostname exercises the 409
// branch directly. TestVerifySucceedsAndSettlesCompetingClaims proves the
// losing claim gets answered, but its competing row is "pending" throughout
// and gets settled by the UPDATE inside FailCompetingClaims — it never
// attempts a second verified row, so it never touches
// domain_hostname_verified_key. That index only fires when a second team
// verifies a hostname another team has already verified, which is what this
// test does: verify "mine" to completion, then have the other team verify
// "theirs" on the very same hostname.
func TestVerifyRefusesASecondTeamsAlreadyVerifiedHostname(t *testing.T) {
	f := newTenancyFixture(t)
	f.domainVerifier.reason = domainverify.ReasonNone

	hostname := "double-verify-" + uuid.NewString()[:8] + ".verein.test"
	mine := claimDomain(t, f, hostname)
	theirs := claimDomainAs(t, f, f.otherAdmin, f.otherTeamID, hostname)

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+mine.ID.String()+"/verify", nil)
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.otherAdmin, http.MethodPost,
		"/v1/domains/"+theirs.ID.String()+"/verify", nil)
	require.Equal(t, http.StatusConflict, second.Code, "body: %s", second.Body.String())

	var status string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select verification_status from domain where id = $1`, theirs.ID).Scan(&status))
	require.NotEqual(t, "verified", status,
		"a team must not walk away believing it owns a hostname another team already verified")
}

// TestVerifyAlreadyVerifiedSkipsTheProbe pins the ordering of the
// already-verified short-circuit: it must run strictly before Check is ever
// called. Without a test for this, the check is one refactor away from
// sliding below the Check call — every other test would stay green, since
// they all set the stub to ReasonNone, but a verified domain would then be
// one flaky DNS lookup away from being un-verified by a later probe.
func TestVerifyAlreadyVerifiedSkipsTheProbe(t *testing.T) {
	f := newTenancyFixture(t)
	f.domainVerifier.reason = domainverify.ReasonNone

	hostname := "already-verified-" + uuid.NewString()[:8] + ".verein.test"
	claim := claimDomain(t, f, hostname)

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())
	require.Equal(t, "verified", decode[struct {
		Domain api.Domain `json:"domain"`
	}](t, first).Domain.VerificationStatus)
	require.Equal(t, 1, f.domainVerifier.calls)

	// If a later probe would fail, that must never be allowed to matter.
	f.domainVerifier.reason = domainverify.ReasonUnreachable

	second := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)
	require.Equal(t, http.StatusOK, second.Code, "body: %s", second.Body.String())
	body := decode[struct {
		Domain api.Domain `json:"domain"`
		Reason string     `json:"reason"`
	}](t, second)
	require.Equal(t, "verified", body.Domain.VerificationStatus)
	require.Empty(t, body.Reason)
	require.Equal(t, 1, f.domainVerifier.calls,
		"an already-verified domain must not be re-probed, so calls must stay at 1")
}

func TestVerifyIsRefusedBelowAdmin(t *testing.T) {
	f := newTenancyFixture(t)
	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestVerifyDomainIsRateLimitedPerDomain pins the domain axis of
// allowDomainVerify in isolation from the user axis: two different admins of
// the same team (f.members[authz.RoleAdmin] and f.members[authz.RoleOwner],
// both clearing DomainAdminScope's threshold) call verify on the very same
// domain. The second caller's own per-user count is still fresh — this can
// only fail if the shared domain-keyed limit is what is doing the blocking.
func TestVerifyDomainIsRateLimitedPerDomain(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.DomainVerifyRateLimitPerHour = 1
	f.rebuildRouter()
	f.domainVerifier.reason = domainverify.ReasonNone

	hostname := "rate-limit-domain-" + uuid.NewString()[:8] + ".verein.test"
	claim := claimDomain(t, f, hostname)

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleOwner], http.MethodPost,
		"/v1/domains/"+claim.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusTooManyRequests, second.Code, "body: %s", second.Body.String())
}

// TestVerifyDomainIsRateLimitedPerUser pins the user axis in isolation from
// the domain axis: the same admin calls verify on two different domains.
// Each domain is only ever checked once — the domain-keyed limit alone would
// let both through — so this can only fail if the shared user-keyed limit is
// what is doing the blocking. This is the sole bound on how many outbound
// DNS/TLS probes one authenticated admin can trigger, so an unexercised user
// axis is not a theoretical gap.
func TestVerifyDomainIsRateLimitedPerUser(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.DomainVerifyRateLimitPerHour = 1
	f.rebuildRouter()
	f.domainVerifier.reason = domainverify.ReasonTokenMissing

	suffix := uuid.NewString()[:8]
	first := claimDomain(t, f, "rate-limit-user-a-"+suffix+".verein.test")
	second := claimDomain(t, f, "rate-limit-user-b-"+suffix+".verein.test")

	firstResp := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+first.ID.String()+"/verify", nil)
	require.Equal(t, http.StatusOK, firstResp.Code, "body: %s", firstResp.Body.String())

	secondResp := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/domains/"+second.ID.String()+"/verify", nil)

	require.Equal(t, http.StatusTooManyRequests, secondResp.Code, "body: %s", secondResp.Body.String())
}

func TestDeleteDomainIsRefusedWhileLinksExist(t *testing.T) {
	// link.domain_id is on delete cascade, and link_click_stats has no raw
	// click table behind it — those rollups cannot be recomputed from
	// anything. "Impossible" is worth more here than "warned".
	f := newTenancyFixture(t)
	// domain_hostname_verified_key is a globally unique partial index on
	// hostname where verification_status = 'verified' — a bare literal here
	// would collide with a row a killed run left behind, wedging every later
	// run at the verify step below until someone cleans the database by hand.
	claim := verifiedDomain(t, f, "has-links-"+uuid.NewString()[:8]+".verein.test")
	createLinkOn(t, f, claim.ID)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		"/v1/domains/"+claim.ID.String(), nil)

	require.Equal(t, http.StatusConflict, rec.Code)

	// The count is what tells the team how much work removing it is, and the
	// frontend reads it back as a typed value (apps/web/src/lib/api-errors.ts),
	// not by pattern-matching the prose in `detail` — so assert the same thing
	// it does: the ErrorDetail's `value`, keyed by its `location`, not a digit
	// somewhere in the body. A reworded `detail` message must not be able to
	// break this.
	body := decode[struct {
		Errors []struct {
			Location string `json:"location"`
			Value    int64  `json:"value"`
		} `json:"errors"`
	}](t, rec)
	require.Len(t, body.Errors, 1)
	require.Equal(t, "path.domain_id", body.Errors[0].Location)
	require.Equal(t, int64(1), body.Errors[0].Value)

	var stillThere int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from domain where id = $1`, claim.ID).Scan(&stillThere))
	require.Equal(t, 1, stillThere)

	// The domain row surviving is not the thing that cannot be recomputed —
	// the link and its rollups are. Assert the link itself is still there,
	// promoting the falsification probe (see task-10-report.md) into a
	// permanent assertion.
	var linksRemaining int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from link where domain_id = $1`, claim.ID).Scan(&linksRemaining))
	require.Equal(t, 1, linksRemaining, "the link must survive a refused delete")
}

func TestDeleteDomainSucceedsWhenEmpty(t *testing.T) {
	f := newTenancyFixture(t)
	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		"/v1/domains/"+claim.ID.String(), nil)

	require.Equal(t, http.StatusNoContent, rec.Code)

	var hostname string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select metadata->>'hostname' from audit_log
		 where action = 'domain.deleted' and entity_id = $1`, claim.ID).Scan(&hostname))
	require.Equal(t, claim.Hostname, hostname,
		"the audit row must name the hostname, since entity_id alone points at a row that is gone")
}

func TestDeleteDomainIsRefusedBelowAdmin(t *testing.T) {
	f := newTenancyFixture(t)
	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/domains/"+claim.ID.String(), nil)

	require.Equal(t, http.StatusForbidden, rec.Code)

	var stillThere int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from domain where id = $1`, claim.ID).Scan(&stillThere))
	require.Equal(t, 1, stillThere)
}

func TestDeleteDomainIs404ForANonMember(t *testing.T) {
	f := newTenancyFixture(t)
	claim := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.stranger, http.MethodDelete, "/v1/domains/"+claim.ID.String(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// claimDomain claims a hostname for the fixture's own team as its admin.
func claimDomain(t *testing.T, f *tenancyFixture, hostname string) api.Domain {
	t.Helper()
	return claimDomainAs(t, f, f.members[authz.RoleAdmin], f.teamID, hostname)
}

func claimDomainAs(
	t *testing.T, f *tenancyFixture, as testUser, teamID uuid.UUID, hostname string,
) api.Domain {
	t.Helper()
	rec := f.do(t, as, http.MethodPost, "/v1/teams/"+teamID.String()+"/domains",
		map[string]string{"hostname": hostname})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[api.Domain](t, rec)
}

// verifiedDomain claims a hostname and marks it verified directly, because
// the HTTP path to verified needs DNS this test has no control over.
func verifiedDomain(t *testing.T, f *tenancyFixture, hostname string) api.Domain {
	t.Helper()
	claimed := claimDomain(t, f, hostname)
	_, err := f.pool.Exec(t.Context(),
		`update domain set verification_status = 'verified', verified_at = now() where id = $1`,
		claimed.ID)
	require.NoError(t, err)
	claimed.VerificationStatus = "verified"
	return claimed
}

// createLinkOn puts one link on a domain, so the delete guard has something
// to refuse over.
func createLinkOn(t *testing.T, f *tenancyFixture, domainID uuid.UUID) {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/", "domain_id": domainID})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
}
