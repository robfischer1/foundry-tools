package main

// THE BUILD LANE'S RECORD, AS PHASES. Until 2026-09-28 the lane answered the
// door with an exit code and nothing else: `build holds — no atom record —
// settle only` was the whole of what ci_logs could say about a build, at every
// depth. A red build named no step, so the only way to learn which one broke
// was to read 400 lines of pod log.
//
// THE PHASES ARE NOT INVENTED, and that is the reason this shape and not
// another. run() was already a sequence of named methods — detect, stageRelease,
// verify, publish, sbom — each answering the same (code, reason) pair the
// lane settles on. This records what they already answer instead of asking them
// to answer differently, so the atom names are the code's own vocabulary rather
// than a second one laid over it.
//
// THE MACHINERY IS SHARED, in lanerecord.go: the cast lane is the same shape and
// wants the same four things from a record. This file is only what is true of
// BUILD.

// buildGroup is every build atom's group.
const buildGroup = "build"

// buildPhases is the star path in the order run() takes it.
//
// THE BASES PATH IS NOT HERE, deliberately — it is a fanout over whatever sits
// under bases/, one atom per base (baseAtom), and it replaces the star path
// entirely rather than joining it. A repo has bases or it has a star image; no
// run takes both, and runBases sets the fanout flag so Unreached stays empty.
var buildPhases = []string{
	"build:preflight",
	"build:dependencies",
	"build:detect",
	"build:release",
	"build:image",
	"build:verify",
	"build:publish",
	"build:sbom",
}

// baseAtom names one base's whole build, scan, publish and promote.
func baseAtom(base string) string { return "build:base:" + base }

// say prints through the build lane's own printer and keeps the line for the
// phase now running.
func (l *buildLane) say(format string, args ...any) {
	l.phases.say(func(line string) { sayLine(line) }, sprintf(format, args...))
}
