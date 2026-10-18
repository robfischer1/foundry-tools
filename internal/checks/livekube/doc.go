// Package livekube is foundry-tools's own proof of the live_kube substrate
// (checks.TestKubes): the suite in live_test.go sits behind `//go:build
// live_kube`, so the gate lane that grades this tree binds a real etcd and
// kube-apiserver for it and hands it TEST_KUBE_URL, TEST_KUBE_TOKEN and
// TEST_KUBE_CA_FILE. The gate pins foundry-tools at the very commit it grades,
// so every push exercises the substrate it ships: a lane that stops binding the
// API server, or binds one that cannot mint and review a ServiceAccount token,
// turns this package red.
package livekube

// Substrate names the thing the package proves, so the package has a non-test
// file.
const Substrate = "live_kube"
