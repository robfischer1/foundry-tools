package castlane

// The toolchains that can compile a binary repo's binaries.
const (
	RustToolchain = "rust"
	GoToolchain   = "go"
)

// The two refusals a tree earns before anything compiles. They are the Dagger
// cast lane's sentences (cast.go release), word for word, because R20 holds the
// executor to the same verdict and the same reason; the test reads cast.go to
// keep them so.
const (
	RefusedBoth    = "the tree declares both a Cargo.toml and a root go.mod, so the cast cannot tell which toolchain builds its binaries"
	RefusedNeither = "the tree is cast as a binary repo and declares no lane that builds one — no Cargo.toml and no go.mod at the root — so nothing can compile its binaries"
)

// Toolchain is the lane that builds a tree's binaries, a fact about the tree
// (never the record, never the copier template): rust is a Cargo.toml at the
// root, go is a go.mod at the root. A tree declaring both or neither has none,
// and the refusal says why. Two toolchains that each build a `tongs` would ship
// whichever ran last.
func Toolchain(rust, goRoot bool) (toolchain, refusal string) {
	switch {
	case rust && goRoot:
		return "", RefusedBoth
	case rust:
		return RustToolchain, ""
	case goRoot:
		return GoToolchain, ""
	}
	return "", RefusedNeither
}
