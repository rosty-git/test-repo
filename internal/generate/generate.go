// Package generate produces the English base description (with an explicit
// fact selection) and the German/French descriptions generated from that
// same fact selection. Generating DE/FR from the fact list rather than by
// translating the English text is the main anti-drift decision: it removes
// "translate this paragraph" as a source of dropped or added facts and
// leaves only "write natively from this fact list" as the failure mode,
// which the consistency checker can catch directly.
package generate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rosty-git/test-repo/internal/llm"
	"github.com/rosty-git/test-repo/internal/model"
)

// Selection is the English base description plus the exact set of fact IDs
// it draws on. This fact ID list is the ground truth every other language
// is checked against.
type Selection struct {
	FactIDs []string `json:"fact_ids"`
	Text    string   `json:"text"`
}

var selectionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"fact_ids": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "IDs of the facts (from the catalog) that the description text states.",
		},
		"text": map[string]any{
			"type":        "string",
			"description": "The English marketing description.",
		},
	},
	"required": []string{"fact_ids", "text"},
}

var descriptionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"text": map[string]any{
			"type":        "string",
			"description": "The marketing description in the target language.",
		},
	},
	"required": []string{"text"},
}

func catalogBlock(facts []model.Fact) string {
	var b strings.Builder
	byCategory := map[string][]model.Fact{}
	var order []string
	for _, f := range facts {
		if _, ok := byCategory[f.Category]; !ok {
			order = append(order, f.Category)
		}
		byCategory[f.Category] = append(byCategory[f.Category], f)
	}
	sort.Strings(order)
	for _, cat := range order {
		fmt.Fprintf(&b, "%s:\n", cat)
		for _, f := range byCategory[cat] {
			tag := ""
			if f.Mandatory {
				tag = " [mandatory]"
			}
			fmt.Fprintf(&b, "  - %s: %s%s\n", f.ID, f.Text, tag)
		}
	}
	return b.String()
}

// EnglishBase asks the model to choose a subset of the fact catalog
// (all mandatory facts plus 4-7 optional ones) and write the base English
// description from exactly that subset.
func EnglishBase(ctx context.Context, client llm.Client, hotel model.Hotel) (Selection, error) {
	facts := hotel.Facts()
	system := "You are a senior travel copywriter producing factual, trustworthy marketing copy for a hotel booking platform. " +
		"You never invent amenities, policies, or claims that are not explicitly given to you."

	user := fmt.Sprintf(`Hotel fact catalog for %s (%s, %s):

%s

Write a short English marketing description (3-5 sentences, roughly 50-90 words) for this hotel.

Rules:
- Select between 6 and 9 facts IN TOTAL (this total includes the mandatory ones) - no more, no fewer. Do not use every fact in the catalog.
- Every fact tagged [mandatory] must be among them.
- Pick the remaining facts from the catalog above to make the most compelling, coherent pitch.
- The description must state nothing that is not covered by a fact you selected. No invented details, no generic filler claims (e.g. "world-class", "luxurious") unless directly supported by a selected fact.
- Return the exact list of fact IDs (mandatory + chosen optional ones) you used in fact_ids, matching the IDs in the catalog exactly.
- Native, engaging English copy - not a list, not a literal recitation of the facts.`, hotel.Name, hotel.City, hotel.Country, catalogBlock(facts))

	raw, err := client.CallTool(ctx, llm.ToolRequest{
		System:      system,
		User:        user,
		ToolName:    "submit_selection",
		ToolDesc:    "Submit the English description and the fact IDs it uses.",
		ToolSchema:  selectionSchema,
		MaxTokens:   1024,
		Temperature: 0.7,
	})
	if err != nil {
		return Selection{}, fmt.Errorf("generate english base: %w", err)
	}

	var sel Selection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return Selection{}, fmt.Errorf("parse english base output: %w", err)
	}
	return sel, nil
}

// InLanguage generates the description in lang (German or French) from the
// exact set of facts in factIDs. feedback, if non-empty, is prior-attempt
// correction guidance (missing/extra facts, or a nativeness critique) and
// is appended to the prompt for a retry.
func InLanguage(ctx context.Context, client llm.Client, hotel model.Hotel, lang model.Language, factIDs []string, feedback string) (string, error) {
	factByID := hotel.FactByID()
	var b strings.Builder
	for _, id := range factIDs {
		f, ok := factByID[id]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  - %s: %s\n", f.ID, f.Text)
	}

	system := fmt.Sprintf("You are a professional %s travel copywriter producing factual, trustworthy marketing copy for a hotel booking platform. "+
		"You write natively in %s for a native audience - never a literal, word-for-word translation. "+
		"You never invent amenities, policies, or claims that are not explicitly given to you.", lang.Name(), lang.Name())

	user := fmt.Sprintf(`Facts to feature for %s (%s, %s), and only these facts:

%s

Write a short marketing description in native %s (3-5 sentences, roughly 50-90 words).

Rules:
- State every fact listed above. Do not drop any of them.
- State nothing else. No invented details, no extra amenities, policies or claims beyond the list, no generic filler claims unless directly supported by a listed fact.
- This must read as if written natively by a %s travel copywriter, not translated from English. Use natural %s sentence structure, idiom and phrasing, not a calque of English word order.`,
		hotel.Name, hotel.City, hotel.Country, b.String(), lang.Name(), lang.Name(), lang.Name())

	if feedback != "" {
		user += "\n\nYour previous draft had problems. Fix them in this rewrite:\n" + feedback
	}

	raw, err := client.CallTool(ctx, llm.ToolRequest{
		System:      system,
		User:        user,
		ToolName:    "submit_description",
		ToolDesc:    "Submit the description text in the target language.",
		ToolSchema:  descriptionSchema,
		MaxTokens:   1024,
		Temperature: 0.7,
	})
	if err != nil {
		return "", fmt.Errorf("generate %s description: %w", lang, err)
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parse %s output: %w", lang, err)
	}
	return out.Text, nil
}
