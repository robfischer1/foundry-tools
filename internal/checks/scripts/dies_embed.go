// Package scripts carries the python that IS some atoms' tool, embedded as
// strings: a string embed has no read step and no error path, where an
// embed.FS ReadFile had an unreachable error branch the mutation lane could
// never see covered.
package scripts

import _ "embed"

// DiesSchema is dies:schema's validator over the slag v1 and v3 schemas and the fleet records.
//
//go:embed dies_schema.py
var DiesSchema string
