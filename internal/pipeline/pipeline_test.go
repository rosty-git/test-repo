package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/rosty-git/test-repo/internal/llm/llmfake"
	"github.com/rosty-git/test-repo/internal/model"
)

func testHotel() model.Hotel {
	return model.Hotel{
		ID:        "hotel-001",
		Name:      "Strandhaus Aurora",
		City:      "Sylt",
		Country:   "Germany",
		Setting:   "beachfront",
		Amenities: []string{"heated outdoor pool", "spa with sauna (adults only)"},
		Rooms:     []string{"double rooms"},
		Nearby:    []string{"3 minute walk to the beach"},
		Policies:  []string{"breakfast included"},
		PriceBand: "premium",
	}
}

// TestRunHotel_RetriesUntilConsistentAndNative scripts a fake LLM to
// reproduce, deterministically, the two failure modes the brief warns
// about: a dropped fact (German attempt 1) and stilted/translated phrasing
// (French attempt 1) despite perfect factual consistency. It asserts the
// retry loop catches both, feeds back the specific problem, and the final
// attempts pass - i.e. the loop is what gets a lucky-vs-reliable output,
// not a single unchecked generation.
func TestRunHotel_RetriesUntilConsistentAndNative(t *testing.T) {
	fake := llmfake.New()
	factIDs := []string{"core.name", "amenities.0"}

	fake.Enqueue("submit_selection", map[string]any{
		"fact_ids": factIDs,
		"text":     "Strandhaus Aurora welcomes you with a heated outdoor pool in Sylt.",
	})

	// English: passes on attempt 1 (reuses the base text, no submit_description call).
	fake.Enqueue("submit_extraction", map[string]any{
		"matched_fact_ids": factIDs,
	})
	fake.Enqueue("submit_nativeness_score", map[string]any{
		"score": 5, "reasoning": "reads naturally", "issues": []string{},
	})

	// German: attempt 1 drops amenities.0 (the pool).
	fake.Enqueue("submit_description", map[string]any{"text": "Das Strandhaus Aurora liegt in Sylt."})
	fake.Enqueue("submit_extraction", map[string]any{"matched_fact_ids": []string{"core.name"}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 5, "reasoning": "fluent", "issues": []string{}})
	// German: attempt 2 fixes it.
	fake.Enqueue("submit_description", map[string]any{"text": "Das Strandhaus Aurora in Sylt erwartet Sie mit beheiztem Freibad."})
	fake.Enqueue("submit_extraction", map[string]any{"matched_fact_ids": factIDs})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 5, "reasoning": "fluent", "issues": []string{}})

	// French: attempt 1 is factually exact but reads as a literal translation.
	fake.Enqueue("submit_description", map[string]any{"text": "Strandhaus Aurora vous accueille avec une piscine exterieure chauffee a Sylt."})
	fake.Enqueue("submit_extraction", map[string]any{"matched_fact_ids": factIDs})
	fake.Enqueue("submit_nativeness_score", map[string]any{
		"score": 2, "reasoning": "reads like a translation", "issues": []string{"unnatural word order"},
	})
	// French: attempt 2 improves phrasing.
	fake.Enqueue("submit_description", map[string]any{"text": "A Sylt, le Strandhaus Aurora vous accueille autour d'une piscine exterieure chauffee."})
	fake.Enqueue("submit_extraction", map[string]any{"matched_fact_ids": factIDs})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 5, "reasoning": "idiomatic", "issues": []string{}})

	cfg := Config{MaxAttempts: 2, ConsistencyWeight: 0.7, NativenessWeight: 0.3, NativenessPassBar: 4}

	var logs []string
	result, err := RunHotel(context.Background(), fake, cfg, testHotel(), func(msg string) { logs = append(logs, msg) })
	if err != nil {
		t.Fatalf("RunHotel returned error: %v", err)
	}

	en := result.Languages[model.English]
	if len(en.Attempts) != 1 || !en.Final().Pass {
		t.Errorf("expected English to pass on attempt 1, got %d attempts, pass=%v", len(en.Attempts), en.Final().Pass)
	}

	de := result.Languages[model.German]
	if len(de.Attempts) != 2 {
		t.Fatalf("expected German to take 2 attempts, got %d", len(de.Attempts))
	}
	if de.Attempts[0].Pass {
		t.Errorf("expected German attempt 1 to fail (dropped fact)")
	}
	if len(de.Attempts[0].Consistency.Missing) != 1 || de.Attempts[0].Consistency.Missing[0] != "amenities.0" {
		t.Errorf("expected attempt 1 to flag amenities.0 as missing, got %+v", de.Attempts[0].Consistency.Missing)
	}
	if !de.Final().Pass {
		t.Errorf("expected German to pass by attempt 2")
	}
	if de.Attempts[0].OverallScore >= de.Final().OverallScore {
		t.Errorf("expected overall score to improve across attempts: %.2f -> %.2f", de.Attempts[0].OverallScore, de.Final().OverallScore)
	}

	fr := result.Languages[model.French]
	if len(fr.Attempts) != 2 {
		t.Fatalf("expected French to take 2 attempts, got %d", len(fr.Attempts))
	}
	if fr.Attempts[0].Pass {
		t.Errorf("expected French attempt 1 to fail on nativeness despite exact factual match")
	}
	if !fr.Attempts[0].Consistency.ExactMatch {
		t.Errorf("expected French attempt 1 to still be factually exact")
	}
	if !fr.Final().Pass {
		t.Errorf("expected French to pass by attempt 2")
	}

	// The retry prompt for German attempt 2 must actually carry the specific
	// missing fact forward, not just "try again".
	var de2Prompt string
	for _, call := range fake.Calls {
		if call.ToolName == "submit_description" && strings.Contains(call.User, "Sylt, Germany") && strings.Contains(call.User, "heated outdoor pool") && strings.Contains(call.User, "Missing facts") {
			de2Prompt = call.User
			break
		}
	}
	if de2Prompt == "" {
		t.Errorf("expected a retry prompt containing 'Missing facts' feedback with the dropped fact text")
	}

	if len(logs) == 0 {
		t.Errorf("expected progress log lines to be emitted")
	}
}
