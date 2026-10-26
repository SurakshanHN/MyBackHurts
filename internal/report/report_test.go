// internal/report/report_test.go
//
// Tests for report.go (Render).
// All test data uses deterministic generators with jd prefix.

package report

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/prescale/prescale/internal/detector"
)

// jdHarmfulResult creates a deterministic Harmful result using a seeded RNG.
func jdHarmfulResult(seed int64) detector.Result {
	rng := rand.New(rand.NewSource(seed))
	_ = rng
	return detector.Result{
		Verdict:    detector.Harmful,
		Confidence: 0.85,
		Culprit:    "redis-cache",
		Findings: []detector.Finding{
			{Name: "p99_ms", Observed: 385.5, Expected: 198.2, Lower: 190.0, Upper: 206.4},
			{Name: "err_rate", Observed: 0.015, Expected: 0.005, Lower: 0.002, Upper: 0.008},
			{Name: "throughput_gain", Observed: 0.01},
		},
		Alternatives: []detector.Alternative{
			{Hypothesis: "traffic already degrading before scale-up", Status: detector.AltRuledOut, Why: "no shift detected in pre-scale P99Ms or ErrRate"},
			{Hypothesis: "warm-up not yet finished", Status: detector.AltRuledOut, Why: "warmup finished and replicas at target"},
			{Hypothesis: "another dependency caused the degradation", Status: detector.AltRuledOut, Why: "none declared"},
		},
		DataQuality: []string{
			"gap larger than 3x the median sample interval",
		},
		Reasons: []string{
			"P99Ms observed=385.5 > upper bound 206.4 (expected=198.2)",
			"culprit dependency: redis-cache",
		},
	}
}

// jdUnknownResult creates a deterministic Unknown result using a seeded RNG.
func jdUnknownResult(seed int64) detector.Result {
	rng := rand.New(rand.NewSource(seed))
	_ = rng
	return detector.Result{
		Verdict:    detector.Unknown,
		Confidence: 0.00,
		Reasons: []string{
			"fewer than 10 pre-scale samples",
			"warm-up not finished: latest Ready=2 < ToReplicas=4",
		},
		DataQuality: []string{
			"fewer than 10 pre-scale samples (have 4)",
		},
	}
}

// jdHelpedResult creates a deterministic Helped result using a seeded RNG.
func jdHelpedResult(seed int64) detector.Result {
	rng := rand.New(rand.NewSource(seed))
	_ = rng
	return detector.Result{
		Verdict:    detector.Helped,
		Confidence: 0.92,
		Findings: []detector.Finding{
			{Name: "p99_ms", Observed: 105.0, Expected: 102.0, Lower: 95.0, Upper: 110.0},
			{Name: "err_rate", Observed: 0.001, Expected: 0.001, Lower: 0.000, Upper: 0.002},
			{Name: "throughput_gain", Observed: 0.45},
		},
		Alternatives: []detector.Alternative{
			{Hypothesis: "traffic already degrading before scale-up", Status: detector.AltRuledOut, Why: "no shift detected in pre-scale P99Ms or ErrRate"},
			{Hypothesis: "warm-up not yet finished", Status: detector.AltRuledOut, Why: "warmup finished and replicas at target"},
			{Hypothesis: "another dependency caused the degradation", Status: detector.AltRuledOut, Why: "none declared"},
		},
		Reasons: []string{
			"throughput gain 45.0% >= 5.0% threshold",
			"P99Ms observed=105.0 within expected [95.0, 110.0]",
		},
	}
}

// jdUselessResult creates a deterministic Useless result using a seeded RNG.
func jdUselessResult(seed int64) detector.Result {
	rng := rand.New(rand.NewSource(seed))
	_ = rng
	return detector.Result{
		Verdict:    detector.Useless,
		Confidence: 0.78,
		Findings: []detector.Finding{
			{Name: "p99_ms", Observed: 101.0, Expected: 100.0, Lower: 95.0, Upper: 105.0},
			{Name: "err_rate", Observed: 0.001, Expected: 0.001, Lower: 0.000, Upper: 0.002},
			{Name: "throughput_gain", Observed: 0.01},
		},
		Alternatives: []detector.Alternative{
			{Hypothesis: "traffic already degrading before scale-up", Status: detector.AltRuledOut, Why: "no shift detected in pre-scale P99Ms or ErrRate"},
			{Hypothesis: "warm-up not yet finished", Status: detector.AltRuledOut, Why: "warmup finished and replicas at target"},
			{Hypothesis: "another dependency caused the degradation", Status: detector.AltRuledOut, Why: "none declared"},
		},
	}
}

func TestRender_Harmful_Golden(t *testing.T) {
	r := jdHarmfulResult(42)
	got := Render(r)

	want := `Verdict: harmful
Confidence: 0.85 (heuristic score, not a probability)
Findings:
- p99_ms: observed=385.5, expected=198.2, range=[190.0, 206.4]
- err_rate: observed=1.5%, expected=0.5%, range=[0.2%, 0.8%]
- throughput_gain: observed=0.0
Culprit: redis-cache
Alternatives:
- traffic already degrading before scale-up: ruled_out (no shift detected in pre-scale P99Ms or ErrRate)
- warm-up not yet finished: ruled_out (warmup finished and replicas at target)
- another dependency caused the degradation: ruled_out (none declared)
Data quality:
- gap larger than 3x the median sample interval
`

	if got != want {
		t.Errorf("Render() mismatch:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestRender_Unknown_Golden(t *testing.T) {
	r := jdUnknownResult(42)
	got := Render(r)

	want := `Verdict: unknown
Confidence: 0.00 (heuristic score, not a probability)
Data quality:
- fewer than 10 pre-scale samples (have 4)
Why unknown:
- fewer than 10 pre-scale samples
- warm-up not finished: latest Ready=2 < ToReplicas=4
`

	if got != want {
		t.Errorf("Render() mismatch:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestRender_EmptyResult(t *testing.T) {
	var r detector.Result
	got := Render(r)

	want := `Verdict: unknown
Confidence: 0.00 (heuristic score, not a probability)
Why unknown:
- none
`

	if got != want {
		t.Errorf("Render() for empty Result mismatch:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestRender_Determinism(t *testing.T) {
	rHarmful := jdHarmfulResult(99)
	rUnknown := jdUnknownResult(99)

	firstHarmful := Render(rHarmful)
	firstUnknown := Render(rUnknown)

	for i := 0; i < 50; i++ {
		if got := Render(rHarmful); got != firstHarmful {
			t.Fatalf("Harmful render was not deterministic on iteration %d", i)
		}
		if got := Render(rUnknown); got != firstUnknown {
			t.Fatalf("Unknown render was not deterministic on iteration %d", i)
		}
	}
}

func TestRender_NoMutation(t *testing.T) {
	r := jdHarmfulResult(101)
	origFindingsLen := len(r.Findings)
	origAltsLen := len(r.Alternatives)
	origDQLen := len(r.DataQuality)
	origReasonsLen := len(r.Reasons)

	_ = Render(r)

	if len(r.Findings) != origFindingsLen {
		t.Errorf("Findings mutated: got len %d, want %d", len(r.Findings), origFindingsLen)
	}
	if len(r.Alternatives) != origAltsLen {
		t.Errorf("Alternatives mutated: got len %d, want %d", len(r.Alternatives), origAltsLen)
	}
	if len(r.DataQuality) != origDQLen {
		t.Errorf("DataQuality mutated: got len %d, want %d", len(r.DataQuality), origDQLen)
	}
	if len(r.Reasons) != origReasonsLen {
		t.Errorf("Reasons mutated: got len %d, want %d", len(r.Reasons), origReasonsLen)
	}
}

func TestRender_SectionOrder(t *testing.T) {
	r := detector.Result{
		Verdict:    detector.Unknown,
		Confidence: 0.50,
		Findings: []detector.Finding{
			{Name: "p99_ms", Observed: 120.0, Expected: 100.0, Lower: 90.0, Upper: 110.0},
		},
		Culprit: "postgres",
		Alternatives: []detector.Alternative{
			{Hypothesis: "warm-up not yet finished", Status: detector.AltRuledOut, Why: "target met"},
		},
		DataQuality: []string{"thin pre data"},
		Reasons:     []string{"low confidence"},
	}

	out := Render(r)

	idxVerdict := strings.Index(out, "Verdict:")
	idxConfidence := strings.Index(out, "Confidence:")
	idxFindings := strings.Index(out, "Findings:")
	idxCulprit := strings.Index(out, "Culprit:")
	idxAlts := strings.Index(out, "Alternatives:")
	idxDQ := strings.Index(out, "Data quality:")
	idxWhyUnknown := strings.Index(out, "Why unknown:")

	if idxVerdict == -1 || idxConfidence == -1 || idxFindings == -1 ||
		idxCulprit == -1 || idxAlts == -1 || idxDQ == -1 || idxWhyUnknown == -1 {
		t.Fatalf("Expected all sections present in output:\n%s", out)
	}

	if !(idxVerdict < idxConfidence &&
		idxConfidence < idxFindings &&
		idxFindings < idxCulprit &&
		idxCulprit < idxAlts &&
		idxAlts < idxDQ &&
		idxDQ < idxWhyUnknown) {
		t.Errorf("Sections not in required order:\nVerdict: %d, Conf: %d, Find: %d, Culp: %d, Alt: %d, DQ: %d, Why: %d",
			idxVerdict, idxConfidence, idxFindings, idxCulprit, idxAlts, idxDQ, idxWhyUnknown)
	}
}

func TestRender_HelpedAndUseless(t *testing.T) {
	helped := Render(jdHelpedResult(77))
	if !strings.Contains(helped, "Verdict: helped") {
		t.Errorf("Expected 'Verdict: helped' in:\n%s", helped)
	}
	if strings.Contains(helped, "Why unknown:") {
		t.Errorf("Helped result should NOT have 'Why unknown:' section:\n%s", helped)
	}
	if strings.Contains(helped, "Culprit:") {
		t.Errorf("Helped result should NOT have 'Culprit:' line:\n%s", helped)
	}

	useless := Render(jdUselessResult(88))
	if !strings.Contains(useless, "Verdict: useless") {
		t.Errorf("Expected 'Verdict: useless' in:\n%s", useless)
	}
	if strings.Contains(useless, "Why unknown:") {
		t.Errorf("Useless result should NOT have 'Why unknown:' section:\n%s", useless)
	}
}

func TestRender_AlternativeWithoutWhy(t *testing.T) {
	r := detector.Result{
		Verdict:    detector.Helped,
		Confidence: 0.90,
		Alternatives: []detector.Alternative{
			{Hypothesis: "some alternative", Status: detector.AltRuledOut, Why: ""},
		},
	}
	out := Render(r)
	if !strings.Contains(out, "- some alternative: ruled_out\n") {
		t.Errorf("Expected alternative without why to format cleanly, got:\n%s", out)
	}
}

func TestRender_EndToEndWithJudge(t *testing.T) {
	// Create an empty input which Judge will evaluate to Unknown.
	var in detector.Input
	res := detector.Judge(in)
	out := Render(res)

	if !strings.Contains(out, "Verdict: unknown") {
		t.Errorf("Expected 'Verdict: unknown' from Judge(empty), got:\n%s", out)
	}
	if !strings.Contains(out, "Why unknown:") {
		t.Errorf("Expected 'Why unknown:' section from Judge(empty), got:\n%s", out)
	}
	if !strings.Contains(out, "heuristic score, not a probability") {
		t.Errorf("Expected confidence phrase in report, got:\n%s", out)
	}
}

// Ensure time package is used to silence unused import if needed
var _ = time.Second
