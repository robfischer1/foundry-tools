package checks

import "strings"

// GoHasTestFiles reads the output of
//
//	go list -f '{{len .TestGoFiles}}{{len .XTestGoFiles}}' ./...
//
// — one line per package, two counts run together — and answers whether ANY
// package carries a test file. A module where every line is some spelling of
// zero has nothing for `go test` to run, and go would print "[no test files]"
// per package and exit 0; that is the green this check exists to refuse.
func GoHasTestFiles(goListOutput string) bool {
	for _, line := range strings.Split(goListOutput, "\n") {
		if strings.Trim(strings.TrimSpace(line), "0") != "" {
			return true
		}
	}
	return false
}
