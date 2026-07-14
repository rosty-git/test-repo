package evaluate

import (
	"context"
	"testing"

	"github.com/rosty-git/test-repo/internal/llm/llmfake"
	"github.com/rosty-git/test-repo/internal/model"
)

func TestJudgeNativeness_TakesMedianOfThreeSamples(t *testing.T) {
	fake := llmfake.New()
	// Exactly the noisy pattern from the brief: the same text scores 3,
	// then 4, then 3 across independent judge calls - the median must land
	// on 3, not an average (3.33, meaningless for an integer scale) and not
	// whichever sample happened to run first or last.
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 3, "reasoning": "a bit stilted", "issues": []string{"x"}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 4, "reasoning": "reads well", "issues": []string{}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 3, "reasoning": "still a bit stiff", "issues": []string{"y"}})

	got, err := JudgeNativeness(context.Background(), fake, model.German, "Ein Beispieltext.")
	if err != nil {
		t.Fatalf("JudgeNativeness returned error: %v", err)
	}
	if got.Score != 3 {
		t.Errorf("expected median score 3, got %d", got.Score)
	}
	if len(fake.Calls) != 3 {
		t.Fatalf("expected exactly 3 judge calls, got %d", len(fake.Calls))
	}
}

func TestJudgeNativeness_DuplicateLowScoreWinsMedian(t *testing.T) {
	// [3, 3, 5] sorted is [3, 3, 5] - the middle (median) value is the lower
	// of the two distinct scores, matching "we only pass if the description
	// consistently looks good": one generous outlier judge call shouldn't
	// be enough to pass a description two of three judges found mediocre.
	fake := llmfake.New()
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 5, "reasoning": "excellent", "issues": []string{}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 3, "reasoning": "so-so", "issues": []string{}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 3, "reasoning": "so-so again", "issues": []string{}})

	got, err := JudgeNativeness(context.Background(), fake, model.French, "Un texte d'exemple.")
	if err != nil {
		t.Fatalf("JudgeNativeness returned error: %v", err)
	}
	if got.Score != 3 {
		t.Errorf("expected median score 3, got %d", got.Score)
	}
}

func TestJudgeNativeness_DuplicateHighScoreWinsMedian(t *testing.T) {
	// [3, 5, 5] sorted is [3, 5, 5] - the true median is 5 here, since two
	// of the three samples agree it's good. Median is not simply "always
	// take the lowest score seen" - that would make the bar unreachable.
	fake := llmfake.New()
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 5, "reasoning": "excellent", "issues": []string{}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 3, "reasoning": "so-so", "issues": []string{}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 5, "reasoning": "excellent again", "issues": []string{}})

	got, err := JudgeNativeness(context.Background(), fake, model.French, "Un texte d'exemple.")
	if err != nil {
		t.Fatalf("JudgeNativeness returned error: %v", err)
	}
	if got.Score != 5 {
		t.Errorf("expected median score 5, got %d", got.Score)
	}
}

func TestJudgeNativeness_PropagatesSampleError(t *testing.T) {
	fake := llmfake.New()
	// Only 2 of the 3 required responses are queued, so one sample errors.
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 4, "reasoning": "ok", "issues": []string{}})
	fake.Enqueue("submit_nativeness_score", map[string]any{"score": 4, "reasoning": "ok", "issues": []string{}})

	_, err := JudgeNativeness(context.Background(), fake, model.German, "Ein Beispieltext.")
	if err == nil {
		t.Fatalf("expected an error when a judge sample fails, got nil")
	}
}
