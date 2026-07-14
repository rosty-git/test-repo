package generate

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

func TestEnglishBase_ParsesSelection(t *testing.T) {
	fake := llmfake.New()
	fake.Enqueue("submit_selection", map[string]any{
		"fact_ids": []string{"core.name", "core.city", "amenities.0"},
		"text":     "Wake up to the sound of the sea at Strandhaus Aurora in Sylt, Germany, with a heated outdoor pool waiting year-round.",
	})

	sel, err := EnglishBase(context.Background(), fake, testHotel())
	if err != nil {
		t.Fatalf("EnglishBase returned error: %v", err)
	}
	if len(sel.FactIDs) != 3 {
		t.Errorf("expected 3 fact ids, got %v", sel.FactIDs)
	}
	if !strings.Contains(sel.Text, "Strandhaus Aurora") {
		t.Errorf("expected generated text to contain hotel name, got %q", sel.Text)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if !strings.Contains(fake.Calls[0].User, "amenities.0") {
		t.Errorf("expected prompt to include the fact catalog with IDs, got prompt: %s", fake.Calls[0].User)
	}
	if !strings.Contains(fake.Calls[0].User, "[mandatory]") {
		t.Errorf("expected prompt to mark mandatory facts, got prompt: %s", fake.Calls[0].User)
	}
}

func TestInLanguage_IncludesOnlySelectedFacts(t *testing.T) {
	fake := llmfake.New()
	fake.Enqueue("submit_description", map[string]any{
		"text": "Mit direktem Strandzugang begrüßt Sie das Strandhaus Aurora in Sylt.",
	})

	text, err := InLanguage(context.Background(), fake, testHotel(), model.German, []string{"core.name", "core.city"}, "")
	if err != nil {
		t.Fatalf("InLanguage returned error: %v", err)
	}
	if text == "" {
		t.Fatalf("expected non-empty text")
	}

	prompt := fake.Calls[0].User
	if strings.Contains(prompt, "heated outdoor pool") {
		t.Errorf("prompt should only include selected facts, but found an unselected one: %s", prompt)
	}
	if !strings.Contains(prompt, "core.city") {
		t.Errorf("expected selected fact core.city in prompt, got: %s", prompt)
	}
}

func TestInLanguage_PassesFeedbackIntoRetryPrompt(t *testing.T) {
	fake := llmfake.New()
	fake.Enqueue("submit_description", map[string]any{"text": "Le Strandhaus Aurora vous attend a Sylt."})

	feedback := "Missing facts (must be added):\n  - amenities.0: heated outdoor pool\n"
	_, err := InLanguage(context.Background(), fake, testHotel(), model.French, []string{"core.name", "amenities.0"}, feedback)
	if err != nil {
		t.Fatalf("InLanguage returned error: %v", err)
	}

	if !strings.Contains(fake.Calls[0].User, "heated outdoor pool") {
		t.Errorf("expected retry feedback to be included in the prompt, got: %s", fake.Calls[0].User)
	}
}
