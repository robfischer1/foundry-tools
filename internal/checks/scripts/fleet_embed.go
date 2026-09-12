package scripts

import _ "embed"

// FleetOrbitDrift is fleet:orbit-drift's checker, byte for byte the script
// that used to live in a heredoc.
//
//go:embed fleet_orbit_drift.py
var FleetOrbitDrift string
