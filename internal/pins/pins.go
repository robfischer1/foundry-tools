// Package pins is what a lane DECLARES it published, rendered as the one line
// the door reads back off the pod log.
//
// THE WHOLE TRANSPORT IS AN ECHO. A lane prints one `::ourea::{"pins":[…]}`
// line; ourea's settle already fetches the pod's log tail, finds the marker
// there and writes a row to erebus.build_pins. There is no network path from
// the runner to the door, no credential, no extra call, and no edit to any
// shared workflow. A lane that never prints is unaffected — the door reads
// nothing and behaves exactly as it did.
//
// THE DECLARATION IS REFUSED HERE TOO, on the same rule the door applies:
// sha256 and 64 lower-case hex, nothing shorter. A twelve-hex abbreviation
// reads fine in prose and can never be matched against a registry, so a pin
// that cannot be checked is not printed rather than stored half-true. Doing
// the refusal at BOTH ends is deliberate: the lane can say what it dropped
// while it still has the context to say why, and the door is not obliged to
// trust that it did.
//
// STANDARD LIBRARY ONLY. This is imported by the lanes, which run inside the
// engine, and a declaration is not worth a dependency.
package pins

import (
	"encoding/json"
	"regexp"
	"strings"
)

// MarkerPrefix opens a line the door reads. It is the fleet's existing marker
// — ourea's citail already scans for it — and pins are one payload among
// others under it, never its only meaning.
const MarkerPrefix = "::ourea::"

// The three things a lane can publish. Kind is what a query filters on, so a
// fourth needs the door's tap.PinsIn to learn it first — an unknown kind is
// refused there, and a row nothing can read is worse than no row.
const (
	KindImage   = "image"
	KindPackage = "package"
	KindBundle  = "bundle"
)

// A pin has a SIDE: the lane either made the artifact or used it. Both ride the
// same marker because one build knows both at one commit, and the door's
// retention keep-set wants their union — but only the built half is a claim of
// authorship, so the two must stay tellable apart on the wire.
//
// EMPTY IS BUILT, and that is not a convenience. Every lane that declared
// before this field existed was declaring what it PUBLISHED, and those lines
// are still in flight in logs the door has not read yet; a default of "dep"
// would silently reclassify them.
const (
	RoleBuilt = "built"
	RoleDep   = "dep"
)

// Pin is one published artifact. The field names ARE the wire format and the
// door's tap.Pin reads them; they are not free to rename on one side.
type Pin struct {
	// Artifact is the repository without its tag: registry.notusmi.com/rob/ourea.
	Artifact string `json:"artifact"`
	// Digest is the full sha256 the registry minted.
	Digest string `json:"digest"`
	// Tag is what now points at that digest, when anything does.
	Tag string `json:"tag,omitempty"`
	// Kind defaults to image at both ends, because a required field nobody
	// remembers is a field that gets set wrong.
	Kind string `json:"kind,omitempty"`
	// Role is built (this lane published it) or dep (this lane used it).
	// Omitted means built — see the constants.
	Role string `json:"role,omitempty"`
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Line renders the declaration for the pins that are well formed, and names
// the ones it would not print. It answers "" when nothing survives, so a
// caller that prints the line unconditionally still prints nothing.
func Line(in ...Pin) (string, []string) {
	var keep []Pin
	var skipped []string
	for _, p := range in {
		p.Artifact = strings.TrimSpace(p.Artifact)
		p.Digest = strings.TrimSpace(p.Digest)
		p.Tag = strings.TrimSpace(p.Tag)
		p.Kind = strings.TrimSpace(p.Kind)
		p.Role = strings.TrimSpace(p.Role)
		switch {
		case p.Artifact == "":
			skipped = append(skipped, "a pin with no artifact, digest "+p.Digest)
			continue
		case !digestRe.MatchString(p.Digest):
			skipped = append(skipped, p.Artifact+": "+p.Digest+" is not sha256 and 64 hex")
			continue
		}
		if p.Kind == "" {
			p.Kind = KindImage
		}
		if p.Kind != KindImage && p.Kind != KindPackage && p.Kind != KindBundle {
			skipped = append(skipped, p.Artifact+": kind "+p.Kind+" is not image, package or bundle")
			continue
		}
		// AN OMITTED ROLE IS LEFT OMITTED, deliberately, where Kind above is
		// defaulted and written. Both ends already agree that empty means
		// built, so writing it out would add ~16 bytes per pin to the one line
		// whose size is a real constraint — the door reads pins out of the last
		// 64 KiB of the log, and #177 is the incident where this declaration
		// fell outside that window and erebus stayed empty. The bytes buy
		// nothing: the reader defaults it on arrival.
		if p.Role != "" && p.Role != RoleBuilt && p.Role != RoleDep {
			skipped = append(skipped, p.Artifact+": role "+p.Role+" is not built or dep")
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == 0 {
		return "", skipped
	}
	// THE ERROR IS DROPPED BECAUSE THE BRANCH CANNOT BE REACHED, and the
	// mutation gate is what made that explicit: encoding/json fails on
	// channels, funcs, cyclic pointers and NaN, and this value is a slice of
	// four-string structs. A defensive branch nothing can enter is a branch
	// nothing can test — it was here, gremlins put an unkillable mutant in it,
	// and the answer was to delete the branch rather than to soften the test.
	body, _ := json.Marshal(struct {
		Pins []Pin `json:"pins"`
	}{keep})
	return MarkerPrefix + string(body), skipped
}

// Image is the pin a registry push earns: the target it pushed to, which
// carries the tag, and the digest the registry minted for it.
//
// THE TAG IS SPLIT OFF THE TARGET RATHER THAN PASSED, because the caller
// already has exactly one string and splitting it twice is how the two drift.
// A registry host may carry a port (host:5000/rob/x:tag), so the tag is the
// last colon AFTER the last slash — a colon before it belongs to the host.
// THE COMPARISON IS STRICTLY GREATER AND THAT IS LOAD-BEARING. A bare name
// with neither a colon nor a slash puts both indexes at -1; `>=` would take
// the branch and slice target[:-1], which panics. Tested, because a lane that
// crashed while declaring a fact about an artifact it had already published
// would turn a bookkeeping line into a failed build.
func Image(target, digest string) Pin { return of(target, digest, KindImage) }

// Bundle is the same reference, declared as a bundle rather than an image. The
// cast lane publishes app/<name>:stable through mold, and a bundle's digest is
// reaped by exactly the retention that reaps an image's — so the door wants it
// on the same wire, told apart only by kind.
//
// IT IS A SEPARATE CONSTRUCTOR RATHER THAN A KIND ARGUMENT because Kind is a
// closed set the door refuses outside of (tap.PinsIn), and a caller that can
// pass a string can pass a wrong one. Two names cost a line each and make the
// wrong call unwritable.
func Bundle(target, digest string) Pin { return of(target, digest, KindBundle) }

// of splits a target into artifact and tag and stamps the kind. Shared by the
// constructors so the comparison below is reasoned about once.
func of(target, digest, kind string) Pin {
	artifact, tag := target, ""
	if i := strings.LastIndex(target, ":"); i > strings.LastIndex(target, "/") {
		artifact, tag = target[:i], target[i+1:]
	}
	return Pin{Artifact: artifact, Digest: digest, Tag: tag, Kind: kind}
}

// consumedRe finds a reference to the fleet's OWN registry that names a digest.
// Both hostnames route to the one zot (foundry.notusmi.com's repositories land
// at that registry's root), so both are ours and a reference through either is
// a dependency the reaper must keep.
//
// A REFERENCE BY TAG ALONE IS NOT A PIN. The tag's own target is whatever it
// points at today, and retention keeps a movable tag unconditionally — so there
// is nothing for a dep row to hold down. Only a digest names a thing that can
// be reaped out from under this build.
var consumedRe = regexp.MustCompile(
	`(?:registry\.notusmi\.com|foundry\.notusmi\.com)/[A-Za-z0-9._/-]+?(?::[A-Za-z0-9._-]+)?@sha256:[0-9a-f]{64}`)

// Consumed reads one file's text and answers the internal-registry digests it
// depends on, as dep pins.
//
// THE CALLER PASSES DOCKERFILES AND NOTHING ELSE, and that is the whole reason
// this is safe where the census's whole-tree grep was not. forge-inuse greps
// every file and then has to EXCLUDE *_test.go and testdata, because a real
// digest inside a Go table test is not a consumer — its own comment records
// nearly marking one inuse "on the authority of a Go table test". A Dockerfile
// is a build instruction: every digest in one is something this image is
// actually made of, so the hazard does not arise rather than being filtered.
//
// Duplicates collapse: the same base named in two stages of one Dockerfile, or
// in two Dockerfiles, is one dependency.
func Consumed(text string) []Pin {
	var out []Pin
	seen := map[string]bool{}
	for _, ref := range consumedRe.FindAllString(text, -1) {
		// THE @ IS GUARANTEED BY THE PATTERN, so there is no miss to guard.
		// consumedRe only matches text containing `@sha256:` and 64 hex, so
		// LastIndex can never answer -1 here. A guard for it was written, the
		// mutation gate put an unkillable CONDITIONALS_BOUNDARY in it, and the
		// answer is the same one Line() above already records: delete the
		// branch nothing can enter rather than soften the test that cannot
		// reach it.
		at := strings.LastIndex(ref, "@")
		p := Image(ref[:at], ref[at+1:])
		p.Role = RoleDep
		key := p.Artifact + "\x00" + p.Tag + "\x00" + p.Digest
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}
