// Package castrelease is the cast lane's compile, run by the executor: the two
// atoms rust:cast-release and go:cast-release call Rust and Go here. It is the
// Dagger cast lane's castLane.release (cast.go) as os/exec argvs, with the
// decisions shared where they are pure (internal/castlane) and the verdict
// wording copied from it (R20: same verdict, same reason, on both runners).
//
// THE BINARIES ARE THE TREE'S, never a Dockerfile's: every bin target cargo
// says the workspace's default members build, or every main package under
// cmd/. A binary repo's binaries are its product (an image's are an
// implementation detail, which is why the build lane reads the Dockerfile and
// rust:release / go:release stay the image's compile). The cast reads no
// Dockerfile, so a binary repo that also carries one (tron) is cast the same.
//
// The binaries are written flat into the release directory (releasekit.OutDir),
// which the layer-cast capability reads back as /work/release.
package castrelease
