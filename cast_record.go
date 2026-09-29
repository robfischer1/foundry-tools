package main

// THE CAST LANE'S RECORD, AS PHASES — the build lane's shape (lanerecord.go),
// applied to the other procedural lane.
//
// WHAT IT REPLACES is the same nothing: `cast holds — no atom record — settle
// only` at every depth, so a cast that failed named no step. That matters more
// here than it does for a build, because a cast is the step that puts a bundle
// in front of the fleet: "the cast failed" and "the cast failed AT VERIFY,
// after minting" are very different facts about what the world now contains.
//
// THE PHASES ARE THE METHODS run() ALREADY CALLS — payload, pin, stage, mint,
// verify — each answering the same (code, reason) pair the lane settles on.

// castGroup is every cast atom's group.
const castGroup = "cast"

// castPhases is the lane in the order run() takes it.
//
// A DRY RUN STOPS AFTER cast:pin, by design — it builds and pins for real and
// stages, mints and verifies nothing — so those three read as UNREACHED rather
// than passed. That distinction is the reason this lane wants a record at all:
// a dry run that reported a signature as held would be claiming the one thing
// it deliberately did not do.
var castPhases = []string{
	"cast:preflight",
	"cast:record",
	"cast:cosign",
	"cast:payload",
	"cast:pin",
	"cast:stage",
	"cast:mint",
	"cast:verify",
	"cast:ring",
}

// say prints through the cast lane's own printer and keeps the line for the
// phase now running.
func (l *castLane) say(format string, args ...any) {
	l.phases.say(func(line string) { castSayLine(line) }, sprintf(format, args...))
}
