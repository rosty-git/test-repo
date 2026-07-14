// Package evaluate implements the two automatic checks the pipeline is
// judged on: factual consistency against the source fact catalog, and
// nativeness of each language's phrasing. Both run as independent LLM
// calls that never see the generation prompts or each other's output, so
// they cannot simply rubber-stamp the generator's own claims.
package evaluate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rosty-git/test-repo/internal/llm"
	"github.com/rosty-git/test-repo/internal/model"
)

// Extraction is the result of independently mapping a description's
// content back onto the hotel's full fact catalog.
type Extraction struct {
	MatchedFactIDs      stringList `json:"matched_fact_ids"`
	ContradictedFactIDs stringList `json:"contradicted_fact_ids"`
	UnsupportedClaims   stringList `json:"unsupported_claims"`
}

var extractionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"matched_fact_ids": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "IDs of catalog facts the text states accurately, including any qualifiers or restrictions.",
		},
		"contradicted_fact_ids": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "IDs of catalog facts the text references but states inaccurately - a dropped, loosened, or altered qualifier/restriction, or an outright contradiction.",
		},
		"unsupported_claims": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Short quotes/paraphrases of specific claims in the text that are not covered by any catalog fact at all.",
		},
	},
	"required": []string{"matched_fact_ids", "contradicted_fact_ids", "unsupported_claims"},
}

func fullCatalogBlock(facts []model.Fact) string {
	var b strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&b, "  - %s: %s\n", f.ID, f.Text)
	}
	return b.String()
}

// Extract independently maps text back onto hotel's full fact catalog. It
// is given every catalog fact, not just the ones the generator intended to
// use, so it can also catch facts smuggled in from elsewhere in the source
// data that were never part of the selected set.
func Extract(ctx context.Context, client llm.Client, hotel model.Hotel, lang model.Language, text string) (Extraction, error) {
	system := "You are a meticulous fact-checker. You compare marketing copy against a fact catalog and report precisely what matches, " +
		"what is stated inaccurately, and what is not supported by the catalog at all. You do not guess generously - if a qualifier " +
		"or restriction is dropped or loosened, that counts as contradicted, not matched."

	user := fmt.Sprintf(`Fact catalog for %s:

%s

Description text (%s):
"%s"

For every catalog fact, decide whether the text states it accurately (including qualifiers such as "adults only", "ground-floor rooms only", "for up to 4", specific distances/times, etc). Report:
- matched_fact_ids: facts stated accurately.
- contradicted_fact_ids: facts referenced but with a dropped, altered, or loosened qualifier, or an outright contradiction.
- unsupported_claims: specific claims in the text about amenities, rooms, policies, location, or setting that are not covered by any catalog fact.

Do not include a fact in matched_fact_ids if any part of its qualifier is missing or changed - use contradicted_fact_ids for that instead.`,
		hotel.Name, fullCatalogBlock(hotel.Facts()), lang.Name(), text)

	raw, err := client.CallTool(ctx, llm.ToolRequest{
		System:      system,
		User:        user,
		ToolName:    "submit_extraction",
		ToolDesc:    "Submit the fact-checking result.",
		ToolSchema:  extractionSchema,
		MaxTokens:   1024,
		Temperature: 0,
	})
	if err != nil {
		return Extraction{}, fmt.Errorf("extract facts (%s): %w", lang, err)
	}

	var ex Extraction
	if err := json.Unmarshal(raw, &ex); err != nil {
		return Extraction{}, fmt.Errorf("parse extraction output (%s): %w", lang, err)
	}
	return ex, nil
}

// ConsistencyScore compares an extraction against the intended fact set
// (the English base's selection) and reports precision/recall/F1 plus the
// specific IDs/claims responsible for any gap.
type ConsistencyScore struct {
	Selected     []string `json:"selected"`
	Matched      []string `json:"matched"`      // selected facts correctly stated
	Missing      []string `json:"missing"`      // selected facts absent from the text
	Contradicted []string `json:"contradicted"` // selected facts stated inaccurately
	Extra        []string `json:"extra"`        // catalog facts stated but not selected
	Invented     []string `json:"invented"`     // claims not in the catalog at all

	Precision  float64 `json:"precision"`
	Recall     float64 `json:"recall"`
	F1         float64 `json:"f1"`
	ExactMatch bool    `json:"exact_match"`
}

func Score(selectedIDs []string, ex Extraction) ConsistencyScore {
	selected := toSet(selectedIDs)
	matchedSet := toSet(ex.MatchedFactIDs)
	contradictedSet := toSet(ex.ContradictedFactIDs)

	var matched, missing, contradicted, extra []string
	for id := range selected {
		switch {
		case matchedSet[id]:
			matched = append(matched, id)
		case contradictedSet[id]:
			contradicted = append(contradicted, id)
		default:
			missing = append(missing, id)
		}
	}
	for id := range matchedSet {
		if !selected[id] {
			extra = append(extra, id)
		}
	}

	truePositives := float64(len(matched))
	falseNegatives := float64(len(missing) + len(contradicted))
	falsePositives := float64(len(extra) + len(ex.UnsupportedClaims))

	precision := safeDiv(truePositives, truePositives+falsePositives)
	recall := safeDiv(truePositives, truePositives+falseNegatives)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}

	return ConsistencyScore{
		Selected:     selectedIDs,
		Matched:      matched,
		Missing:      missing,
		Contradicted: contradicted,
		Extra:        extra,
		Invented:     ex.UnsupportedClaims,
		Precision:    precision,
		Recall:       recall,
		F1:           f1,
		ExactMatch:   len(missing) == 0 && len(contradicted) == 0 && len(extra) == 0 && len(ex.UnsupportedClaims) == 0,
	}
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 1
	}
	return a / b
}

// Feedback renders a ConsistencyScore as actionable retry guidance for the
// generator prompt.
func (s ConsistencyScore) Feedback(factByID map[string]model.Fact) string {
	var b strings.Builder
	if len(s.Missing) > 0 {
		b.WriteString("Missing facts (must be added):\n")
		for _, id := range s.Missing {
			fmt.Fprintf(&b, "  - %s: %s\n", id, factByID[id].Text)
		}
	}
	if len(s.Contradicted) > 0 {
		b.WriteString("Facts stated inaccurately - fix the qualifier (e.g. restore the restriction/detail exactly):\n")
		for _, id := range s.Contradicted {
			fmt.Fprintf(&b, "  - %s: %s\n", id, factByID[id].Text)
		}
	}
	if len(s.Extra) > 0 {
		b.WriteString("Facts mentioned that were not selected - remove them:\n")
		for _, id := range s.Extra {
			fmt.Fprintf(&b, "  - %s: %s\n", id, factByID[id].Text)
		}
	}
	if len(s.Invented) > 0 {
		b.WriteString("Invented claims not supported by any fact - remove them:\n")
		for _, c := range s.Invented {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	return b.String()
}
