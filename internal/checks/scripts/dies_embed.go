// Package scripts carries the python that IS some atoms' tool, embedded as
// strings: a string embed has no read step and no error path, where an
// embed.FS ReadFile had an unreachable error branch the mutation lane could
// never see covered.
package scripts

import _ "embed"

// DiesDoorProbe is dies:contracts' reachability probe for the door's raw API.
//
//go:embed dies_door_probe.py
var DiesDoorProbe string

// DiesSchema is dies:schema's validator over the slag schema and the v2 records.
//
//go:embed dies_schema.py
var DiesSchema string
