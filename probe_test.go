// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// fileIfPresent's whole job is to never ask for a File the tree does not hold
// as one. A read issued for an absent path or a directory is the errored span
// it exists to remove, so each case also checks which questions were asked.

func fileQueries() (exists, reads int) {
	for _, q := range engine.chains() {
		if strings.Contains(q, "exists(") {
			exists++
		}
		if strings.Contains(q, "contents") {
			reads++
		}
	}
	return exists, reads
}

func TestFileIfPresentReadsARegularFile(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{".copier-answers.yml": "_commit: v1\n"})
	body, present, err := fileIfPresent(t.Context(), dag.Directory(), ".copier-answers.yml")
	if err != nil || !present || body != "_commit: v1\n" {
		t.Fatalf("got (%q, %v, %v), want the body, present, no error", body, present, err)
	}
	if ex, rd := fileQueries(); ex != 1 || rd != 1 {
		t.Errorf("want one exists and one read, got %d exists, %d reads", ex, rd)
	}
}

func TestFileIfPresentDoesNotReadAnAbsentFile(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	body, present, err := fileIfPresent(t.Context(), dag.Directory(), ".copier-answers.yml")
	if err != nil || present || body != "" {
		t.Fatalf("got (%q, %v, %v), want absent with no error", body, present, err)
	}
	if _, rd := fileQueries(); rd != 0 {
		t.Errorf("an absent file must not be read (that read is the errored span), got %d reads", rd)
	}
}

// A primary checkout's .git: a directory is not a regular file, so it is not
// read. This was the most frequent errored span in the fleet.
func TestFileIfPresentDoesNotReadADirectory(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{".git/HEAD": "ref: refs/heads/main\n"})
	_, present, err := fileIfPresent(t.Context(), dag.Directory(), ".git")
	if err != nil || present {
		t.Fatalf("got (%v, %v), want a directory answered as not present", present, err)
	}
	if _, rd := fileQueries(); rd != 0 {
		t.Errorf("a directory must not be read as a file, got %d reads", rd)
	}
}

func TestFileIfPresentPassesTheEnginesFailureOn(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{".copier-answers.yml": "x"})
	engine.failLeaf("", "exists", "engine went away")
	_, present, err := fileIfPresent(t.Context(), dag.Directory(), ".copier-answers.yml")
	if err == nil || present {
		t.Fatalf("got (%v, %v), want the engine's error and not present", present, err)
	}
}

func TestFileIfPresentPassesAFailedReadOn(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{".copier-answers.yml": "x"})
	engine.failLeaf("", "contents", "read failed")
	_, present, err := fileIfPresent(t.Context(), dag.Directory(), ".copier-answers.yml")
	if err == nil || present {
		t.Fatalf("got (%v, %v), want the read's error and not present", present, err)
	}
}

func TestCtrFileIfPresentReadsWhatTheContainerHolds(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"/src/stryker-honest.json": "{}"})
	body, present, err := ctrFileIfPresent(t.Context(), dag.Container(), "/src/stryker-honest.json")
	if err != nil || !present || body != "{}" {
		t.Fatalf("got (%q, %v, %v), want the body", body, present, err)
	}
	_, present, err = ctrFileIfPresent(t.Context(), dag.Container(), "/src/reports/mutation/mutation.json")
	if err != nil || present {
		t.Fatalf("got (%v, %v), want absent with no error", present, err)
	}
}

func TestCtrFileIfPresentPassesTheEnginesFailureOn(t *testing.T) {
	engine.reset()
	engine.failLeaf("", "exists", "engine went away")
	if _, present, err := ctrFileIfPresent(t.Context(), dag.Container(), "/src/x"); err == nil || present {
		t.Fatalf("got (%v, %v), want the engine's error", present, err)
	}
	engine.reset()
	engine.withTree(map[string]string{"/src/x": "y"})
	engine.failLeaf("", "contents", "read failed")
	if _, present, err := ctrFileIfPresent(t.Context(), dag.Container(), "/src/x"); err == nil || present {
		t.Fatalf("got (%v, %v), want the read's error", present, err)
	}
}
