// Package pipeline orchestrates generation and evaluation into a
// generate -> check -> refine loop, and records every attempt so the run
// output is itself the evidence of how the score got to where it did.
package pipeline

import (
	"context"
	"fmt"

	"github.com/rosty-git/test-repo/internal/evaluate"
	"github.com/rosty-git/test-repo/internal/generate"
	"github.com/rosty-git/test-repo/internal/llm"
	"github.com/rosty-git/test-repo/internal/model"
)

type Config struct {
	MaxAttempts       int
	ConsistencyWeight float64 // weight on the factual-consistency F1 in the overall score
	NativenessWeight  float64 // weight on the (score/5) nativeness rating in the overall score
	NativenessPassBar int     // minimum nativeness score (1-5) to count as a pass
}

func DefaultConfig() Config {
	return Config{
		MaxAttempts:       5,
		ConsistencyWeight: 0.7,
		NativenessWeight:  0.3,
		NativenessPassBar: 4,
	}
}

// Attempt is one generate+evaluate cycle for a single language.
type Attempt struct {
	N            int                       `json:"n"`
	Text         string                    `json:"text"`
	Consistency  evaluate.ConsistencyScore `json:"consistency"`
	Nativeness   evaluate.NativenessScore  `json:"nativeness"`
	OverallScore float64                   `json:"overall_score"`
	Pass         bool                      `json:"pass"`
}

type LanguageResult struct {
	Language model.Language `json:"language"`
	Attempts []Attempt      `json:"attempts"`
}

func (lr LanguageResult) Final() Attempt {
	return lr.Attempts[len(lr.Attempts)-1]
}

type HotelResult struct {
	Hotel     model.Hotel                        `json:"hotel"`
	FactIDs   []string                           `json:"selected_fact_ids"`
	Languages map[model.Language]*LanguageResult `json:"languages"`
}

func overallScore(cfg Config, c evaluate.ConsistencyScore, n evaluate.NativenessScore) float64 {
	return cfg.ConsistencyWeight*c.F1 + cfg.NativenessWeight*(float64(n.Score)/5.0)
}

func passes(cfg Config, c evaluate.ConsistencyScore, n evaluate.NativenessScore) bool {
	return c.ExactMatch && n.Score >= cfg.NativenessPassBar
}

// RunHotel generates and scores the English/German/French descriptions for
// one hotel, retrying each language independently (with targeted feedback
// from the previous attempt's checks) until it passes or MaxAttempts is
// reached.
func RunHotel(ctx context.Context, client llm.Client, cfg Config, hotel model.Hotel, log func(string)) (HotelResult, error) {
	sel, err := generate.EnglishBase(ctx, client, hotel)
	if err != nil {
		return HotelResult{}, fmt.Errorf("english base for %s: %w", hotel.ID, err)
	}
	log(fmt.Sprintf("[%s] selected %d facts for English base", hotel.ID, len(sel.FactIDs)))

	factByID := hotel.FactByID()
	result := HotelResult{
		Hotel:     hotel,
		FactIDs:   sel.FactIDs,
		Languages: map[model.Language]*LanguageResult{},
	}

	for _, lang := range model.Languages {
		lr := &LanguageResult{Language: lang}
		text := sel.Text
		feedback := ""

		for attemptN := 1; attemptN <= cfg.MaxAttempts; attemptN++ {
			if attemptN > 1 || lang != model.English {
				// Attempt 1 in English reuses the base generation text;
				// every other case (EN retries, DE/FR attempts) generates fresh.
				var genErr error
				text, genErr = generate.InLanguage(ctx, client, hotel, lang, sel.FactIDs, feedback)
				if genErr != nil {
					return HotelResult{}, fmt.Errorf("%s attempt %d for %s: %w", lang, attemptN, hotel.ID, genErr)
				}
			}

			extraction, err := evaluate.Extract(ctx, client, hotel, lang, text)
			if err != nil {
				return HotelResult{}, fmt.Errorf("extract %s attempt %d for %s: %w", lang, attemptN, hotel.ID, err)
			}
			consistency := evaluate.Score(sel.FactIDs, extraction)

			nativeness, err := evaluate.JudgeNativeness(ctx, client, lang, text)
			if err != nil {
				return HotelResult{}, fmt.Errorf("judge %s attempt %d for %s: %w", lang, attemptN, hotel.ID, err)
			}

			attempt := Attempt{
				N:            attemptN,
				Text:         text,
				Consistency:  consistency,
				Nativeness:   nativeness,
				OverallScore: overallScore(cfg, consistency, nativeness),
				Pass:         passes(cfg, consistency, nativeness),
			}
			lr.Attempts = append(lr.Attempts, attempt)
			log(fmt.Sprintf("[%s/%s] attempt %d: consistency F1=%.2f exact=%v nativeness=%d/5 overall=%.2f pass=%v",
				hotel.ID, lang, attemptN, consistency.F1, consistency.ExactMatch, nativeness.Score, attempt.OverallScore, attempt.Pass))

			if attempt.Pass {
				break
			}

			// Fix facts before polishing phrasing. Feeding both kinds of
			// feedback into one rewrite at once tended to trade fact
			// accuracy for smoother wording (or vice versa) rather than
			// converging - see EVALUATION.md. Once the facts are exact,
			// nativeness fixes are scoped to wording only so they can't
			// silently drop or alter a fact again.
			if !consistency.ExactMatch {
				feedback = consistency.Feedback(factByID)
			} else {
				feedback = "The facts stated are exactly correct - do not add, remove, or alter any fact or qualifier in the rewrite. " +
					"Make the smallest possible edit: change only the specific flagged phrases below, keep the rest of the sentence " +
					"structure and word choices as they are. Do not rewrite the whole text from scratch. Fix:\n"
				for _, issue := range nativeness.Issues {
					feedback += "  - " + issue + "\n"
				}
			}
		}

		result.Languages[lang] = lr
	}

	return result, nil
}
