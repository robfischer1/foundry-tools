package atomlib

import (
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// THE RETRY OF A CHECKER THAT CLONES (DEF-B4-BACKOFF). ops:specs' console-specs,
// with no sibling checkout, runs a live `git clone --depth 1
// http://ourea:8215/nas01-stacks.git`, and exits 2 when it could not read the
// source. A resolver or a door a few seconds late is not a verdict about the
// tree: the checker is run again after 1, 2, 4 and 8 s, each moved up to 30%
// either way (15 s, 19.5 s at the worst jitter), and only when its output names
// a network error. Any other 2 (a repository that is not there, a 404) is final.

// netWaits are the waits between a checker's runs.
var netWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// netJitter is the fraction either way a wait moves.
const netJitter = 0.3

// netSleep and netRand are the seams a test replaces.
var (
	netSleep = time.Sleep
	netRand  = rand.Float64
)

// netWait is d moved by up to 30% either way.
func netWait(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (1 + netJitter*(2*netRand()-1)))
}

// netMarkers are what git, curl and python say of a network that was not there
// a moment ago, lower case.
var netMarkers = []string{
	"could not resolve host", "temporary failure in name resolution", "name or service not known",
	"connection refused", "connection reset", "failed to connect", "couldn't connect",
	"timed out", "network is unreachable", "no route to host",
	"returned error: 5", "early eof", "remote end hung up", "rpc failed", "unexpected disconnect",
}

// transientNet says whether lowered output names a network error worth a retry.
func transientNet(lowered string) bool {
	return slices.ContainsFunc(netMarkers, func(m string) bool { return strings.Contains(lowered, m) })
}

// runNet is run, again after each wait while it exits 2 on a network error.
func runNet(run func() (string, int)) (string, int) {
	out, code := run()
	for _, w := range netWaits {
		if code != 2 || !transientNet(strings.ToLower(out)) {
			break
		}
		netSleep(netWait(w))
		out, code = run()
	}
	return out, code
}
