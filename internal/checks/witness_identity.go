package checks

import (
	"errors"
	"strconv"
	"strings"
)

// WitnessMTLSURL is narcissus's mTLS door: the plaintext port plus one, as
// every star's DualListener serves it. fleet:witness asks here, and only here, when the lane
// has, presenting the run's SVID (spiffe://notusmi.com/job/<lane>/<job>); a
// bare name for the reason WitnessURL gives. It is the ONLY door an ask goes to:
// WitnessURL is read for /ready and nothing else.
const WitnessMTLSURL = "https://narcissus:8201/mcp"

// WitnessNoAnswerExit is witnesscall's exit when the identified request WENT
// OUT and the witness gave no answer in time (deadline, or an answer cut
// short). Repeating it cannot help: the witness is slow, and the retry only
// doubles its load. The atom settles it could-not-consult instead.
const WitnessNoAnswerExit = 4

// ErrWitnessNoAnswer marks an identified ask the witness left unanswered. It
// is never retried.
var ErrWitnessNoAnswer = errors.New("the witness did not answer the identified ask in time")

// ParseWitnessCall reads witnesscall post's stdout: "HTTP <status>", the
// content type, then the body, one per line. output() trims the end, so an
// answer with neither content type nor body is the status line alone.
func ParseWitnessCall(out string) (int, string, string, error) {
	statusLine, rest, _ := strings.Cut(out, "\n")
	digits, ok := strings.CutPrefix(statusLine, "HTTP ")
	status, err := strconv.Atoi(digits)
	if !ok || err != nil {
		return 0, "", "", errors.New("witnesscall printed no status line: " + statusLine)
	}
	ctype, body, _ := strings.Cut(rest, "\n")
	return status, ctype, body, nil
}

// WitnessAskedAs is the atom's identity line: the lane's SVID and the door it
// was presented at. There is no other line; an ask with no identity is never made.
func WitnessAskedAs(id string) string {
	return "identity: asked as " + id + " at " + WitnessMTLSURL
}
