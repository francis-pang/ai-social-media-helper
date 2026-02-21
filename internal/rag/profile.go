package rag

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type DecisionStats struct {
	TotalSessions       int
	TotalDecisions      int
	KeepRate            float64
	OverrideRate        float64
	KeepReasonCounts    map[string]int
	DiscardReasonCounts map[string]int
	OverridePatterns    []string
	RecentOverrides     []OverrideDecision
	MediaTypeBreakdown  map[string]map[string]int
}

func ComputeStats(triageDecisions []TriageDecision, overrideDecisions []OverrideDecision, selectionDecisions []SelectionDecision) DecisionStats {
	stats := DecisionStats{
		KeepReasonCounts:    make(map[string]int),
		DiscardReasonCounts: make(map[string]int),
		OverridePatterns:    []string{},
		RecentOverrides:     []OverrideDecision{},
		MediaTypeBreakdown:  make(map[string]map[string]int),
	}

	sessionSet := make(map[string]bool)
	for _, d := range triageDecisions {
		sessionSet[d.SessionID] = true
		stats.TotalDecisions++

		mt := d.MediaType
		if mt == "" {
			mt = "unknown"
		}
		if stats.MediaTypeBreakdown[mt] == nil {
			stats.MediaTypeBreakdown[mt] = map[string]int{"kept": 0, "discarded": 0}
		}
		if d.Saveable {
			stats.MediaTypeBreakdown[mt]["kept"]++
			if d.Reason != "" {
				stats.KeepReasonCounts[d.Reason]++
			}
		} else {
			stats.MediaTypeBreakdown[mt]["discarded"]++
			if d.Reason != "" {
				stats.DiscardReasonCounts[d.Reason]++
			}
		}
	}
	stats.TotalSessions = len(sessionSet)

	if stats.TotalDecisions > 0 {
		kept := 0
		for _, d := range triageDecisions {
			if d.Saveable {
				kept++
			}
		}
		stats.KeepRate = float64(kept) / float64(stats.TotalDecisions)
	}

	overrideCount := 0
	for _, d := range overrideDecisions {
		if d.IsFinalized {
			overrideCount++
		}
		if d.AIVerdict != "" && d.Action != "" {
			stats.OverridePatterns = append(stats.OverridePatterns, fmt.Sprintf("%s -> %s: %s", d.AIVerdict, d.Action, d.AIReason))
		}
	}
	if stats.TotalDecisions > 0 {
		stats.OverrideRate = float64(overrideCount) / float64(stats.TotalDecisions)
	}

	sorted := make([]OverrideDecision, len(overrideDecisions))
	copy(sorted, overrideDecisions)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt > sorted[j].CreatedAt
	})
	n := 10
	if len(sorted) < n {
		n = len(sorted)
	}
	stats.RecentOverrides = sorted[:n]

	return stats
}

func FormatStatsForLLM(stats DecisionStats) string {
	statsJSON, _ := json.Marshal(map[string]interface{}{
		"totalSessions":       stats.TotalSessions,
		"totalDecisions":      stats.TotalDecisions,
		"keepRate":            stats.KeepRate,
		"overrideRate":        stats.OverrideRate,
		"keepReasonCounts":    stats.KeepReasonCounts,
		"discardReasonCounts": stats.DiscardReasonCounts,
		"overridePatterns":    stats.OverridePatterns,
		"mediaTypeBreakdown":  stats.MediaTypeBreakdown,
	})

	var sb strings.Builder
	sb.WriteString("Given the following statistics about a user's media curation preferences, ")
	sb.WriteString("write a concise preference profile. Use bullet points. Be specific. Do not invent patterns not present in the data.\n\n")
	sb.WriteString("Stats:\n")
	sb.WriteString(string(statsJSON))
	return sb.String()
}
