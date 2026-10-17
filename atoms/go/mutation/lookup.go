package gomutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// THE ATOM ASKS BEFORE IT GRADES (incremental mutation, M4). Under the run's
// record token it posts its units' keys to the door it was fetched from
// (/ci/mutants, which the door relays to Daedalus beside /ci/record) and grades
// only the units no stored grading answers. EVERY FAILURE GRADES COLD: no token,
// no door, a door or runner that does not answer, an answer that is not exactly
// about the units asked — each is a lookup with no hits, said on the verdict, and
// never a skip, a red or a could-not-run.
//
// The credential reaches the atom as ATOM_MUTANTS_TOKEN and is taken out of the
// environment before anything else runs (atoms.TakeToken). It is armed only on a
// lane that asked to reuse gradings: the Dagger adapter sets it under --reuse
// with a --record-token, the executor's atom environment for a mutation lane
// whose Job carries a record token.

// mutantsPath is where a lane asks. Stated in three repositories — here,
// ourea's relay, Daedalus's jobs.MutantsPath — because none imports another.
const mutantsPath = "/ci/mutants"

// lookupTimeout bounds one lookup; a store slower than this is a store that did
// not answer, and the atom grades cold.
const lookupTimeout = 20 * time.Second

// lookupAnswerLimit bounds what of an answer is read: an answer cut by it does
// not parse, and the atom grades cold.
const lookupAnswerLimit = 8 << 20

// replyLineCap bounds how much of the door's reply reaches the verdict.
const replyLineCap = 200

// gradingLookup asks the store which keyed units are already graded.
type gradingLookup func(ctx context.Context, lang, engine string, keys []checks.UnitKey) (map[string]checks.ReusedGrading, error)

// auditThreshold is the sampled share of reuse runs that grade cold anyway and
// audit (M6): a commit whose sha's sha256 starts with a byte below it — 13 of
// 256, 5.1%. Keyed on the commit, so a re-run of one commit is audited or not
// the same way.
const auditThreshold = 13

// auditSampled reports whether a reuse run on this commit is audited.
func auditSampled(sha string) bool {
	sum := sha256.Sum256([]byte(sha))
	return sum[0] < auditThreshold
}

// mutantsEndpoint is the door's /ci/mutants, derived from the repository URL the
// tree was fetched from exactly as the record post's endpoint is: scheme and
// host, then the path. An origin that is not a URL gets the bare path, which no
// client can reach — so its lookup fails, and it grades cold.
func mutantsEndpoint(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return mutantsPath
	}
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, mutantsPath)
}

// lookupVia is a gradingLookup over the door, authorised by the run's record
// token. An empty token asks the door nothing it would believe, and fails the
// way the door's refusal does.
func lookupVia(origin, token string) gradingLookup {
	return func(ctx context.Context, lang, engine string, keys []checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		endpoint := mutantsEndpoint(origin)
		ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		// NO ERROR TO HANDLE: the method is a constant and the endpoint is a URL
		// mutantsEndpoint built, or a bare path, both of which parse.
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(checks.LookupBody(lang, engine, keys)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("POST %s could not reach the door: %v", endpoint, strings.ReplaceAll(err.Error(), token, "<token>"))
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, lookupAnswerLimit))
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("POST %s → %d: %s", endpoint, resp.StatusCode, firstLine(strings.ReplaceAll(string(raw), token, "<token>")))
		}
		return checks.AcceptLookup(lang, keys, raw)
	}
}

// firstLine is the reply's first line, trimmed and capped: enough for a
// refusal's reason, never a page of HTML.
func firstLine(reply string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(reply), "\n")
	line = strings.TrimSpace(line)
	if len(line) > replyLineCap {
		line = line[:replyLineCap] + "…"
	}
	return line
}
