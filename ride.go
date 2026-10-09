package main

import (
	"context"
	"fmt"
	"time"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// A RIDER IS A SECOND LANE GRADED INSIDE THE GATE JOB (F6b, "orbit rides the
// gate Job"). Orbit's atoms take under a second; its own Job took a p50 19s,
// nearly all of it pod, engine session and module load. Grading it in the gate's
// call, on the same session and the same fetched tree, keeps the atoms and drops
// the overhead. Its record is still its own: graded under its own lane name and
// posted under its own token, because Daedalus binds one token to one record and
// a second post under the gate's token would OVERWRITE the gate's.
//
// THE RIDER CANNOT TOUCH THE GATE. It grades on a copy of the module, in a
// goroutine that recovers a panic, and it is waited on only after the gate's
// record is posted and its shadow reported, for at most rideGrace. Nothing it
// does reaches the gate's record, file, error or exit; it adds lines of its own,
// each prefixed `ride <lane>:`, after the gate's.

// rideLanes are the lanes allowed to ride, by laneOf. Orbit only, for now:
// mutation runs under a different service account and a far longer deadline,
// and the gate does not ride itself.
var rideLanes = map[string]bool{"orbit": true}

// rideGrace is how long the gate waits for its rider AFTER the gate's own record
// is posted and its shadow has reported. The rider started before the gate
// graded, so the grace is only the tail; past it the rider is abandoned unposted
// and the gate returns, and the rider's lane settles however the door settles a
// lane that sent no record.
var rideGrace = 60 * time.Second

// rideHandle is a started rider. A nil handle (no ride asked, or a ride
// refused) is valid and finish on it does nothing.
type rideHandle struct {
	lane   string
	repo   string
	token  *dagger.Secret
	grace  time.Duration
	done   chan *StageResult
	cancel context.CancelFunc
}

// rideRefusal is why a ride cannot be taken, or "" when it can. Every refusal
// rides nothing and costs the gate one stderr line.
func rideRefusal(stage, ride string, token *dagger.Secret) string {
	lane := laneOf(ride)
	switch {
	case lane == "gate":
		// laneOf answers "gate" for any stage it does not know, so "gate",
		// "prepush" and a typo all land here.
		return fmt.Sprintf("%q grades as the gate lane, which does not ride itself", ride)
	case !rideLanes[lane]:
		return fmt.Sprintf("the %s lane does not ride the gate Job (only orbit does; mutation keeps its own service account and deadline)", lane)
	case laneOf(stage) != "gate":
		return fmt.Sprintf("only the gate Job carries a rider, and this is the %s lane", laneOf(stage))
	case token == nil:
		return "no --ride-token: one token binds one record, so the rider cannot post under the gate's"
	}
	return ""
}

// startRide starts grading the rider's stage in a goroutine and answers at once.
// With no ride it does nothing at all: no goroutine, no line.
//
// THE RIDER GRADES A COPY OF m. Its fields are the gate's (the same fetched
// tree, the same spire socket) EXCEPT the ballot box, which belongs to the
// gate's shadow: a rider casting into it would put orbit's votes in the gate's
// shadow report. The reuse lookup is the mutation lane's and is dropped too.
func (m *FoundryTools) startRide(ctx context.Context, tree, stage, ride, base string, token *dagger.Secret) *rideHandle {
	if ride == "" {
		return nil
	}
	if why := rideRefusal(stage, ride, token); why != "" {
		_, _ = fmt.Fprintf(recordPostOut, "ride %s: refused - %s; riding nothing\n", ride, why)
		return nil
	}
	rider := *m
	rider.box, rider.lookup, rider.audit = nil, nil, false
	h := &rideHandle{lane: laneOf(ride), repo: m.Repo, token: token, grace: rideGrace, done: make(chan *StageResult, 1)}
	ctx, h.cancel = context.WithCancel(ctx)
	// Buffered: an abandoned rider can still answer and its goroutine end.
	go func() {
		// A PANIC IS A COULD-NOT-RUN RECORD for the rider's lane, never the
		// gate's death (the pattern atoms_vote.go's poll uses).
		defer func() {
			if p := recover(); p != nil {
				reason := fmt.Sprintf("the %s rider panicked inside the gate Job: %v", h.lane, p)
				h.done <- stageResult(checks.SettleStage(h.lane, checks.CannotRunVector(h.lane, gradedStage(ride), reason)))
			}
		}()
		h.done <- rider.gateStage(ctx, tree, ride, base)
	}()
	return h
}

// finish waits at most the grace for the rider, then posts its record under the
// rider's own token and prints what the door said. A rider past the grace is
// abandoned unposted. The post's failure is a line, as the gate's is.
func (h *rideHandle) finish(ctx context.Context) {
	if h == nil {
		return
	}
	defer h.cancel()
	timer := time.NewTimer(h.grace)
	defer timer.Stop()
	select {
	case result := <-h.done:
		// Record() cannot fail on a StageResult (see GateFile), and an empty
		// record is refused by sendRecord, which says so.
		record, _ := result.Record()
		// h.token is non-nil (rideRefusal refuses a ride without one), so
		// widening it cannot make the typed-nil interface postRecord warns of.
		_, _ = fmt.Fprintln(recordPostOut, "ride "+h.lane+": record post: "+recordPostOutcome(ctx, h.repo, h.token, record))
	case <-timer.C:
		_, _ = fmt.Fprintf(recordPostOut, "ride %s: not posted - no answer within the %s grace after the gate posted\n", h.lane, h.grace)
	}
}
