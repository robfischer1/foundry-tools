package atoms

import (
	"context"
	"time"
)

// Witness is how fleet:witness reaches narcissus. The zero value is the
// production port and the real clock.
type Witness struct {
	// Sleep is the pause between attempts; nil is checks.SleepContext.
	Sleep func(context.Context, time.Duration) error
	// Call runs the identity helper (Name "witnesscall": whoami, then get for
	// /ready and post for each ask);
	// nil asks the lane pod's SPIRE agent in this process (witnesscall.Exec).
	Call Exec
}
