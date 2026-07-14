package evaluate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/rosty-git/test-repo/internal/llm"
	"github.com/rosty-git/test-repo/internal/model"
)

// nativenessSamples is the number of independent judge calls JudgeNativeness
// takes the median of. A single temperature-0 judge call still finds a
// different nitpick each time the text changes slightly, which made the same
// description score e.g. 3, then 4, then 3 again across retries - noise in
// the judge, not a real change in the text. Sampling 3x and taking the
// median smooths that out; odd so there's always a well-defined middle
// value with no averaging needed.
const nativenessSamples = 3

// NativenessScore is an LLM-judge rating of whether a text reads as if
// written directly by a native speaker, on a 1-5 scale. It is intentionally
// scored independently of the consistency check: nativeness is about
// phrasing, not content.
type NativenessScore struct {
	Score     int        `json:"score"`
	Reasoning string     `json:"reasoning"`
	Issues    stringList `json:"issues"`
}

var nativenessSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"score": map[string]any{
			"type":        "integer",
			"description": "1 (reads as a stilted, literal translation) to 5 (indistinguishable from copy written directly by a native marketing writer).",
		},
		"reasoning": map[string]any{
			"type": "string",
		},
		"issues": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Specific phrases that feel translated, calqued, or non-idiomatic, if any.",
		},
	},
	"required": []string{"score", "reasoning", "issues"},
}

// JudgeNativeness runs nativenessSamples independent judge calls in
// parallel and returns the one whose Score is the median, to smooth out
// single-call judge noise. With an odd sample count the median is always a
// single, well-defined middle value once sorted - ties only arise when two
// samples share the same score, and sorting ascending naturally resolves
// those in favor of the lower value sitting at the middle index (e.g.
// [3,3,4] -> 3; a description only counts as consistently good if a
// majority of samples agree it's good).
func JudgeNativeness(ctx context.Context, client llm.Client, lang model.Language, text string) (NativenessScore, error) {
	scores := make([]NativenessScore, nativenessSamples)
	errs := make([]error, nativenessSamples)

	var wg sync.WaitGroup
	for i := 0; i < nativenessSamples; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scores[i], errs[i] = judgeNativenessOnce(ctx, client, lang, text)
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return NativenessScore{}, err
		}
	}

	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Score < scores[j].Score })
	return scores[nativenessSamples/2], nil
}

func judgeNativenessOnce(ctx context.Context, client llm.Client, lang model.Language, text string) (NativenessScore, error) {
	system := fmt.Sprintf("You are a native %s copy editor for a travel booking platform with a sharp eye for translated-sounding text: "+
		"calques of English word order, unnatural collocations, over-literal idiom, register that doesn't fit marketing copy in %s.",
		lang.Name(), lang.Name())

	user := fmt.Sprintf(`Rate this %s hotel marketing description on a 1-5 scale for how natively it reads:

"%s"

5 = indistinguishable from copy a native %s marketing writer would produce from scratch.
3 = understandable and mostly fluent but has a few translated-sounding phrases or slightly off word choices.
1 = clearly a literal or machine translation - awkward syntax, wrong collocations, unnatural phrasing throughout.

List any specific phrases that feel translated, if there are any.`, lang.Name(), text, lang.Name())

	raw, err := client.CallTool(ctx, llm.ToolRequest{
		System:      system,
		User:        user,
		ToolName:    "submit_nativeness_score",
		ToolDesc:    "Submit the nativeness rating.",
		ToolSchema:  nativenessSchema,
		MaxTokens:   512,
		Temperature: 0,
	})
	if err != nil {
		return NativenessScore{}, fmt.Errorf("judge nativeness (%s): %w", lang, err)
	}

	var score NativenessScore
	if err := json.Unmarshal(raw, &score); err != nil {
		return NativenessScore{}, fmt.Errorf("parse nativeness output (%s): %w", lang, err)
	}
	return score, nil
}
