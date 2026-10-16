package atoms

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// Witness is how fleet:witness reaches narcissus. The zero value is the
// production port and the real clock.
type Witness struct {
	// URL is the plaintext port the witness's /ready is read on; "" is
	// checks.WitnessURL. No ask is ever posted to it.
	URL string
	// Sleep is the pause between attempts; nil is checks.SleepContext.
	Sleep func(context.Context, time.Duration) error
	// Probe reads the witness's /ready before any ask; nil is checks.GetWitnessReady.
	Probe checks.WitnessProbe
}
