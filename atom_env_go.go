package main

import (
	"context"
	"slices"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE GO SUITES' ENVIRONMENT. The question the suite asks is in its atom
// (atoms/go/test, test-race, diff-coverage); what stays here is the place it is
// asked in: the gravity binary the regen tests need on PATH, and the test
// servers the tree's build tags name, bound to the lane container as Dagger
// services. Both are decided off the tree, by the same reads the atom makes, and
// neither judges anything.

// goGravity stages the generator the tree's regen tests ask for (withGravityRegen
// carries the reasoning). A tree whose provenance cannot be read or honoured is
// left as it is: the atom reads the same provenance and files that itself.
func goGravity(ctx context.Context, r *run, ctr *dagger.Container) *dagger.Container {
	staged, _, err := r.withGravityRegen(ctx, ctr)
	if err != nil {
		return ctr
	}
	return staged
}

// goSuiteLane binds, to the container the suite runs in, the gravity binary and
// the servers the tree's suites need. A scope names the lane's own servers: two
// atoms that run the suite at once (go:test-race and go:diff-coverage) must not
// share a database (bindTestDatabases says why). The servers are left unbound
// for the unit suite, which compiles no tag.
func goSuiteLane(scope string, servers bool) func(context.Context, *run, *dagger.Container) *dagger.Container {
	return func(ctx context.Context, r *run, ctr *dagger.Container) *dagger.Container {
		ctr = goGravity(ctx, r, ctr)
		if !servers {
			return ctr
		}
		return goServersLane(scope)(ctx, r, ctr)
	}
}

// goServersLane binds the test servers the tree's suites name under a lane's own
// alias and nothing else: go:mutation grades by running the suite, and stages no
// generator of its own.
func goServersLane(scope string) func(context.Context, *run, *dagger.Container) *dagger.Container {
	return func(ctx context.Context, r *run, ctr *dagger.Container) *dagger.Container {
		// every module is read in its own directory, as its suite is run there;
		// the lane binds what any of them asks for
		dirs, err := r.goModuleDirs(ctx)
		if err != nil {
			return ctr
		}
		var dbs []checks.TestDB
		var brokers []checks.TestBroker
		for _, dir := range dirs {
			module := inModule(ctr, dir)
			found, _ := r.testDatabases(ctx, module)
			dbs = append(dbs, found...)
			wanted, _ := r.testBrokers(ctx, module)
			brokers = append(brokers, wanted...)
		}
		return bindTestBrokers(bindTestDatabases(ctr, vocabularyDBs(dbs), scope), vocabularyBrokers(brokers), scope)
	}
}

// vocabularyDBs is the databases found, each once, in the vocabulary's binding
// order (checks.TestDBsFor's order).
func vocabularyDBs(found []checks.TestDB) []checks.TestDB {
	var out []checks.TestDB
	for _, d := range checks.TestDBs {
		if slices.ContainsFunc(found, func(f checks.TestDB) bool { return f.Tag == d.Tag }) {
			out = append(out, d)
		}
	}
	return out
}

// vocabularyBrokers is the brokers found, each once, in the vocabulary's order.
func vocabularyBrokers(found []checks.TestBroker) []checks.TestBroker {
	var out []checks.TestBroker
	for _, b := range checks.TestBrokers {
		if slices.ContainsFunc(found, func(f checks.TestBroker) bool { return f.Tag == b.Tag }) {
			out = append(out, b)
		}
	}
	return out
}
