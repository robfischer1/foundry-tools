package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE WITNESS ASKS AS THE LANE'S RUN, WHEN THE LANE GAVE IT ONE.
//
// fleetWitness's POST ran in this module's own process, inside the dagger
// ENGINE, so narcissus saw the engine's pod IP and no certificate: measured
// 2026-10-04 (tcpdump on dev01), the four witness SYNs of a gate came from
// dagger-engine-qs6vv and none from the lane pod, and narcissus recorded every
// gate's ask as `unidentified`. Under enforce it would refuse every gate.
//
// With --spire the lane pod forwards its SPIRE agent socket. The agent
// attests the lane pod, and its entry (flux ci-jobs ClusterSPIFFEID) names
// the run: spiffe://notusmi.com/job/gate/<job>. witnesscall fetches that SVID
// into its own memory and asks narcissus's mTLS door with it — the key is
// never a file, a secret or a layer.
//
// NEVER A VERDICT ABOUT IDENTITY. While stars witness, no socket or no SVID
// is the same ask in the clear, and the atom's output says which way it asked.
// An identified ask that could not be made (connect, TLS) also falls back. One
// that WAS sent and drew no answer in time (witnesscall exit 4) does not: the
// witness is slow, the clear retry doubled its load and filed 2465 anonymous
// `unidentified` records on 2026-10-07. That ask settles could-not-consult.

// withSpire hands the run the lane pod's forwarded SPIRE socket (nil: none).
func (r *run) withSpire(s *dagger.Socket) *run {
	r.spire = s
	return r
}

// witnessCaller is the container witnesscall runs in: the binary built from
// this module's source, on the static base, with the forwarded socket owned
// by the base's nonroot user (build.go hadesCaller says why). stamp keys every
// exec to this run, so no cached exec answers a later one.
func witnessCaller(spire *dagger.Socket, stamp string) *dagger.Container {
	return dag.Container().From(checks.ImageStatic).
		WithFile("/usr/local/bin/witnesscall", helperBinary("witnesscall")).
		WithUnixSocket("/run/spire/agent.sock", spire, dagger.ContainerWithUnixSocketOpts{Owner: "65532:65532"}).
		WithEnvVariable("WITNESSCALL_SOCKET", "unix:///run/spire/agent.sock").
		WithEnvVariable("WITNESS_RUN", stamp)
}

// witnessSleep is the identified ask's pause between attempts; a variable so
// the tests do not wait it out.
var witnessSleep = checks.SleepContext

// witnessStamp is this run's cache key for the identified execs.
var witnessStamp = func() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }

// witnessAsk is how one run asks narcissus: ask posts one request, and say
// is the line the atom's output carries about the identity it asked as.
type witnessAsk struct {
	ask func(context.Context, string) (int, string, string, error)
	say func() string
}

// witnessAsker answers how this run asks narcissus.
func (r *run) witnessAsker(ctx context.Context) witnessAsk {
	inClear := func(why string) witnessAsk {
		return witnessAsk{ask: askWitness, say: func() string { return checks.WitnessInTheClear(why) }}
	}
	if r.spire == nil {
		return inClear("the lane forwarded no --spire socket")
	}
	ctr := witnessCaller(r.spire, witnessStamp())
	id, code, err := output(ctx, ctr.WithExec([]string{"/usr/local/bin/witnesscall", "whoami"}, anyExit))
	if err != nil {
		return inClear("witnesscall never ran: " + err.Error())
	}
	if code != 0 {
		return inClear(id)
	}
	var fell, asked atomic.Int64
	ask := func(ctx context.Context, body string) (int, string, string, error) {
		asked.Add(1)
		status, ctype, answer, err := checks.AskWitnessRetried(ctx, identifiedPost(ctr), body,
			checks.WitnessAttempts, checks.WitnessRetryPause, witnessSleep)
		if err == nil {
			return status, ctype, answer, nil
		}
		if errors.Is(err, checks.ErrWitnessNoAnswer) {
			// The identified request went out and the witness is slow. The
			// clear port would repeat it, double the load and file an
			// `unidentified` caller; the atom settles could-not-consult.
			return 0, "", "", err
		}
		fell.Add(1)
		return askWitness(ctx, body)
	}
	say := func() string { return checks.WitnessAskedAs(id, fell.Load(), asked.Load()) }
	return witnessAsk{ask: ask, say: say}
}

// identifiedPost posts one request through witnesscall at narcissus's mTLS
// door. Anything but an answer is an error, which the caller retries and then
// asks in the clear.
func identifiedPost(ctr *dagger.Container) func(context.Context, string) (int, string, string, error) {
	return func(ctx context.Context, body string) (int, string, string, error) {
		out, code, err := output(ctx, ctr.
			WithNewFile("/tmp/witness-request.json", body).
			WithExec([]string{"/usr/local/bin/witnesscall", "post", checks.WitnessMTLSURL, "/tmp/witness-request.json"}, anyExit))
		if err != nil {
			return 0, "", "", err
		}
		if code == checks.WitnessNoAnswerExit {
			return 0, "", "", fmt.Errorf("%w: %s", checks.ErrWitnessNoAnswer, out)
		}
		if code != 0 {
			return 0, "", "", errors.New(out)
		}
		return checks.ParseWitnessCall(out)
	}
}
