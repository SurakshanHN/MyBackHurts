package main

import (
	"strings"
	"testing"
)

func TestDemo_Deterministic(t *testing.T) {
	out1 := runDemo()
	out2 := runDemo()

	if out1 != out2 {
		t.Fatalf("runDemo output is not deterministic between runs:\nrun 1:\n%s\nrun 2:\n%s", out1, out2)
	}

	expectedSubstrings := []string{
		"Alert check: max dependency saturation = 50%, threshold = 90%, alert fires: false",
		"Verdict: harmful",
		"Culprit: db",
		"Control verdict: unknown",
		"Verdict: unknown",
		"Healthy-surge verdict: helped",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(out1, sub) {
			t.Errorf("output missing expected substring %q", sub)
		}
	}
}
