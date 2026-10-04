package checks

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// WitnessMTLSURL is narcissus's mTLS door: the plaintext port plus one, as
// every star's DualListener serves it. fleet:witness asks here when the lane
// forwarded its SPIRE socket, presenting the run's SVID
// (spiffe://notusmi.com/job/<lane>/<job>); a bare name for the reason
// WitnessURL gives.
const WitnessMTLSURL = "https://narcissus:8201/mcp"

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

// WitnessAskedAs is the atom's identity line when it asked as the lane's
// SVID; fell of asked asks went to the clear port after the identified ask
// failed.
func WitnessAskedAs(id string, fell, asked int64) string {
	line := "identity: asked as " + id + " at " + WitnessMTLSURL
	if fell > 0 {
		line += fmt.Sprintf(" (%d of %d ask(s) fell back to %s in the clear)", fell, asked, WitnessURL)
	}
	return line
}

// WitnessInTheClear is the atom's identity line when it asked with no
// identity, and why. Never a finding: stars are in witness mode.
func WitnessInTheClear(why string) string {
	return "identity: none — asked " + WitnessURL + " in the clear: " + why
}
