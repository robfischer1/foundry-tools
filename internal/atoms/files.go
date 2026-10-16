package atoms

import "dagger/foundry-tools/internal/checks"

// The scan verdicts live in internal/checks (CannotEnumerate, Interrupted,
// Unmeasured), where the atoms in their own packages read them too.
var cannotEnumerate = checks.CannotEnumerate
