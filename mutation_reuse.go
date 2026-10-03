package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// THE LANE ASKS BEFORE IT GRADES (incremental mutation, M4). Under --reuse
// the mutation lane posts its units' keys to the door it cloned from
// (/ci/mutants, which the door relays to Daedalus beside /ci/record),
// authorised by the run's own record token, and grades only the units no
// stored grading answers. EVERY FAILURE GRADES COLD: no token, no door, a
// door or runner that does not answer, an answer that is not exactly about
// the units asked — each is a lookup with no hits, said on the verdict, and
// never a skip, a red or a could-not-run.

// mutantsPath is where a lane asks. Stated in three repositories — here,
// ourea's relay, Daedalus's jobs.MutantsPath — because none imports another.
const mutantsPath = "/ci/mutants"

// lookupTimeout bounds one lookup; a store slower than this is a store that
// did not answer, and the lane grades cold.
const lookupTimeout = 20 * time.Second

// lookupAnswerLimit bounds what of an answer is read: an answer cut by it
// does not parse, and the lane grades cold.
const lookupAnswerLimit = 8 << 20

// gradingLookup asks the store which keyed units are already graded.
type gradingLookup func(ctx context.Context, lang, engine string, keys []checks.UnitKey) (map[string]checks.ReusedGrading, error)

// withLookup arms a run's mutation atoms to reuse stored gradings; nil grades
// every unit, as every run did before --reuse.
func (r *run) withLookup(l gradingLookup) *run {
	r.lookup = l
	return r
}

// withAudit makes a reuse run grade cold and audit what it would have reused.
func (r *run) withAudit(audit bool) *run {
	r.audit = audit
	return r
}

// auditThreshold is the sampled share of reuse runs that grade cold anyway
// and audit (M6): a commit whose sha's sha256 starts with a byte below it —
// 13 of 256, 5.1%. Keyed on the commit, so a re-run of one commit is
// audited or not the same way.
const auditThreshold = 13

// auditSampled reports whether a reuse run on this commit is audited.
func auditSampled(sha string) bool {
	sum := sha256.Sum256([]byte(sha))
	return sum[0] < auditThreshold
}

// reusable answers the gradings a run may reuse for its keys, and a line for
// the verdict when it asked and was not answered. No lookup asks nothing.
func (r *run) reusable(ctx context.Context, lang, engine string, keys []checks.UnitKey) (map[string]checks.ReusedGrading, string) {
	if r.lookup == nil {
		return nil, ""
	}
	got, err := r.lookup(ctx, lang, engine, keys)
	if err != nil {
		return nil, "reuse: the store did not answer, so every unit was graded cold — " + err.Error()
	}
	return got, ""
}

// mutantsEndpoint is the door's /ci/mutants, derived from the repo URL the
// run was built with exactly as the record post's is (recordEndpoint). A run
// built with no repo URL gets the bare path, which no client can reach — so
// its lookup fails, and it grades cold.
func mutantsEndpoint(repo string) string {
	record, _ := recordEndpoint(repo)
	return strings.TrimSuffix(record, recordPostPath) + mutantsPath
}

// lookupVia is a gradingLookup over the door, authorised by the run's record
// token.
func lookupVia(repo string, token plaintexter) gradingLookup {
	return func(ctx context.Context, lang, engine string, keys []checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		endpoint := mutantsEndpoint(repo)
		secret, err := token.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("the record token could not be read: %v", err)
		}
		ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		// NO ERROR TO HANDLE: the method is a constant and the endpoint is a URL
		// recordEndpoint built, or a bare path, both of which parse.
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(checks.LookupBody(lang, engine, keys)))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("POST %s could not reach the door: %v", endpoint, err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, lookupAnswerLimit))
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("POST %s → %d: %s", endpoint, resp.StatusCode, firstLine(scrub(string(raw), secret)))
		}
		return checks.AcceptLookup(lang, keys, raw)
	}
}
