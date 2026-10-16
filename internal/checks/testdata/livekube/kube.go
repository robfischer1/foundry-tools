// Package kubefixture is the lane-level proof for the live_kube substrate: a
// module whose only live test sits behind the live_kube build tag and talks to the
// real API server the lane binds. The test lives in kube_live_test.go.fixture so
// foundry-tools' own lanes, which read build tags recursively, do not bind an API
// server for this tree; `just live-kube-proof` copies it into place and runs the
// real go:test-race atom over the copy.
package kubefixture

// Version names the fixture, so the package has a non-test file.
const Version = "live-kube-proof@1"
