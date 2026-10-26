// Package report renders human-readable plain-English reports from detector results.
package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/prescale/prescale/internal/detector"
)

// Render formats a detector.Result into a deterministic plain-English report.
// It never panics and does not output timestamps.
//
// Sections are formatted in the following order:
//  1. Verdict line
//  2. Confidence line (with "heuristic score, not a probability")
//  3. Findings with observed/expected/range numbers (%.1f, err rates as %)
//  4. Culprit (if any)
//  5. One line per alternative with status and why
//  6. Data quality lines (if any)
//  7. For Unknown, a "Why unknown" section listing Reasons
func Render(r detector.Result) string {
	_ = sort.Strings

	var b strings.Builder

	// 1. Verdict line.
	verdict := r.Verdict
	if verdict == "" {
		verdict = detector.Unknown
	}
	b.WriteString(fmt.Sprintf("Verdict: %s\n", verdict))

	// 2. Confidence line.
	b.WriteString(fmt.Sprintf("Confidence: %.2f (heuristic score, not a probability)\n", r.Confidence))

	// 3. Findings section.
	if len(r.Findings) > 0 {
		b.WriteString("Findings:\n")
		for _, f := range r.Findings {
			isErr := strings.Contains(strings.ToLower(f.Name), "err")
			if isErr {
				if f.Expected == 0 && f.Lower == 0 && f.Upper == 0 {
					b.WriteString(fmt.Sprintf("- %s: observed=%.1f%%\n", f.Name, f.Observed*100))
				} else {
					b.WriteString(fmt.Sprintf("- %s: observed=%.1f%%, expected=%.1f%%, range=[%.1f%%, %.1f%%]\n",
						f.Name, f.Observed*100, f.Expected*100, f.Lower*100, f.Upper*100))
				}
			} else {
				if f.Expected == 0 && f.Lower == 0 && f.Upper == 0 {
					b.WriteString(fmt.Sprintf("- %s: observed=%.1f\n", f.Name, f.Observed))
				} else {
					b.WriteString(fmt.Sprintf("- %s: observed=%.1f, expected=%.1f, range=[%.1f, %.1f]\n",
						f.Name, f.Observed, f.Expected, f.Lower, f.Upper))
				}
			}
		}
	}

	// 4. Culprit section (if any).
	if r.Culprit != "" {
		b.WriteString(fmt.Sprintf("Culprit: %s\n", r.Culprit))
	}

	// 5. Alternatives section.
	if len(r.Alternatives) > 0 {
		b.WriteString("Alternatives:\n")
		for _, a := range r.Alternatives {
			if a.Why != "" {
				b.WriteString(fmt.Sprintf("- %s: %s (%s)\n", a.Hypothesis, a.Status, a.Why))
			} else {
				b.WriteString(fmt.Sprintf("- %s: %s\n", a.Hypothesis, a.Status))
			}
		}
	}

	// 6. Data quality lines.
	if len(r.DataQuality) > 0 {
		b.WriteString("Data quality:\n")
		for _, dq := range r.DataQuality {
			b.WriteString(fmt.Sprintf("- %s\n", dq))
		}
	}

	// 7. Why unknown section (for Unknown).
	if verdict == detector.Unknown {
		b.WriteString("Why unknown:\n")
		if len(r.Reasons) == 0 {
			b.WriteString("- none\n")
		} else {
			for _, reason := range r.Reasons {
				b.WriteString(fmt.Sprintf("- %s\n", reason))
			}
		}
	}

	return b.String()
}
