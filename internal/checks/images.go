package checks

// The image map — the toolchain lives in the module, not on a laptop.
//
// ONE PLACE, deliberately. Every lane image is named here so digest pinning is
// a single edit rather than a sweep: the fleet has been broken twice by a
// floating base, and `digest-pins` is the sweep that will land on this block.
// Tags today, digests when that sweep arrives.
const (
	imageGo     = "docker.io/library/golang:1.26-bookworm"
	imagePython = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
	imageRust   = "docker.io/library/rust:1-bookworm"
	imageTS     = "docker.io/oven/bun:1-debian"
	imageFleet  = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
)

// StocksRepo / StocksRef pin the ONE definition of the checks that are scripts
// rather than tool invocations.
//
// A copy would be the defect the script itself exists to catch: on 2026-08-16 a
// sweep found 533 noqa on one rule, 368 of them eight decisions replicated into
// 46 repos by a scaffold pour. `stop_justifications.py` has exactly one home,
// and this reads it there through the door rather than vendoring a second.
const (
	StocksRepo = "https://git.notusmi.com/foundry/foundry-stocks.git"
	StocksRef  = "main"
)
