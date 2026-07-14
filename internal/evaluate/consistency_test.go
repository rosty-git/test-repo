package evaluate

import "testing"

func TestScore_ExactMatch(t *testing.T) {
	selected := []string{"core.name", "amenities.0", "amenities.1"}
	ex := Extraction{MatchedFactIDs: []string{"core.name", "amenities.0", "amenities.1"}}

	got := Score(selected, ex)

	if !got.ExactMatch {
		t.Errorf("expected exact match, got %+v", got)
	}
	if got.F1 != 1 {
		t.Errorf("expected F1=1, got %f", got.F1)
	}
	if len(got.Missing) != 0 || len(got.Extra) != 0 || len(got.Contradicted) != 0 || len(got.Invented) != 0 {
		t.Errorf("expected no discrepancies, got %+v", got)
	}
}

func TestScore_MissingFact(t *testing.T) {
	// The German draft dropped "amenities.1" (e.g. the pool) - the checker
	// must catch this as a dropped fact, not silently pass.
	selected := []string{"core.name", "amenities.0", "amenities.1"}
	ex := Extraction{MatchedFactIDs: []string{"core.name", "amenities.0"}}

	got := Score(selected, ex)

	if got.ExactMatch {
		t.Fatalf("expected not exact match when a fact is dropped")
	}
	if len(got.Missing) != 1 || got.Missing[0] != "amenities.1" {
		t.Errorf("expected amenities.1 flagged missing, got %+v", got.Missing)
	}
	if got.Recall >= 1 {
		t.Errorf("expected recall < 1, got %f", got.Recall)
	}
}

func TestScore_InventedClaim(t *testing.T) {
	// The French draft added a spa the source never mentioned - unsupported
	// claim, must tank precision even though every selected fact is present.
	selected := []string{"core.name", "amenities.0"}
	ex := Extraction{
		MatchedFactIDs:    []string{"core.name", "amenities.0"},
		UnsupportedClaims: []string{"luxurious spa with hot stone massages"},
	}

	got := Score(selected, ex)

	if got.ExactMatch {
		t.Fatalf("expected not exact match when there is an invented claim")
	}
	if len(got.Invented) != 1 {
		t.Errorf("expected 1 invented claim recorded, got %+v", got.Invented)
	}
	if got.Precision >= 1 {
		t.Errorf("expected precision < 1 due to invented claim, got %f", got.Precision)
	}
}

func TestScore_ContradictedQualifier(t *testing.T) {
	// The source says "dogs allowed in ground-floor rooms only"; a draft
	// that says "dogs allowed" (dropping the restriction) must be flagged
	// as contradicted, not matched - this is the case a naive keyword/
	// substring check would miss.
	selected := []string{"policies.1"}
	ex := Extraction{ContradictedFactIDs: []string{"policies.1"}}

	got := Score(selected, ex)

	if got.ExactMatch {
		t.Fatalf("expected not exact match when a qualifier is dropped")
	}
	if len(got.Contradicted) != 1 || got.Contradicted[0] != "policies.1" {
		t.Errorf("expected policies.1 flagged contradicted, got %+v", got.Contradicted)
	}
	if len(got.Missing) != 0 {
		t.Errorf("a contradicted fact should not also be counted as missing, got %+v", got.Missing)
	}
}

func TestScore_ExtraFact(t *testing.T) {
	// The German draft mentions EV charging, which is real (in the source
	// catalog) but was not one of the facts the English base selected -
	// this is the "drift between languages" failure mode from the brief.
	selected := []string{"core.name"}
	ex := Extraction{MatchedFactIDs: []string{"core.name", "amenities.4"}}

	got := Score(selected, ex)

	if got.ExactMatch {
		t.Fatalf("expected not exact match when an unselected fact is added")
	}
	if len(got.Extra) != 1 || got.Extra[0] != "amenities.4" {
		t.Errorf("expected amenities.4 flagged extra, got %+v", got.Extra)
	}
}

func TestScore_EmptySelectionDoesNotDivideByZero(t *testing.T) {
	got := Score(nil, Extraction{})
	if !got.ExactMatch {
		t.Errorf("expected exact match for empty selection with empty extraction")
	}
	if got.Recall != 1 || got.Precision != 1 {
		t.Errorf("expected recall=precision=1 for the vacuous case, got %+v", got)
	}
}
