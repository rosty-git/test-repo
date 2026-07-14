// Package model defines the source data shape and the canonical fact
// catalog derived from it. The fact catalog is the ground truth that every
// downstream check (selection, generation, extraction, scoring) is anchored
// to, so hotel data and fact IDs live together in one place.
package model

import (
	"encoding/json"
	"fmt"
	"os"
)

type Hotel struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	City      string   `json:"city"`
	Country   string   `json:"country"`
	Setting   string   `json:"setting"`
	Amenities []string `json:"amenities"`
	Rooms     []string `json:"rooms"`
	Nearby    []string `json:"nearby"`
	Policies  []string `json:"policies"`
	PriceBand string   `json:"price_band"`
}

// Fact is one atomic, independently checkable claim about a hotel.
// ID is stable across a run (e.g. "amenities.2") so extraction results
// from different languages can be compared set-wise.
type Fact struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Text      string `json:"text"`
	Mandatory bool   `json:"mandatory"`
}

// Facts returns the full canonical fact catalog for the hotel. Name/city/
// country are mandatory: a description that dropped or contradicted them
// wouldn't be describing this hotel. Everything else is optional material
// the generator may choose to feature or skip.
func (h Hotel) Facts() []Fact {
	facts := []Fact{
		{ID: "core.name", Category: "core", Text: fmt.Sprintf("The hotel is called %s.", h.Name), Mandatory: true},
		{ID: "core.city", Category: "core", Text: fmt.Sprintf("It is located in %s, %s.", h.City, h.Country), Mandatory: true},
		{ID: "core.setting", Category: "core", Text: fmt.Sprintf("The setting is: %s.", h.Setting), Mandatory: false},
		{ID: "core.price_band", Category: "core", Text: fmt.Sprintf("The price band is: %s.", h.PriceBand), Mandatory: false},
	}
	facts = append(facts, factsFor("amenities", h.Amenities)...)
	facts = append(facts, factsFor("rooms", h.Rooms)...)
	facts = append(facts, factsFor("nearby", h.Nearby)...)
	facts = append(facts, factsFor("policies", h.Policies)...)
	return facts
}

func factsFor(category string, items []string) []Fact {
	out := make([]Fact, 0, len(items))
	for i, item := range items {
		out = append(out, Fact{
			ID:       fmt.Sprintf("%s.%d", category, i),
			Category: category,
			Text:     item,
		})
	}
	return out
}

// FactByID indexes a hotel's fact catalog for lookups during scoring.
func (h Hotel) FactByID() map[string]Fact {
	m := make(map[string]Fact)
	for _, f := range h.Facts() {
		m[f.ID] = f
	}
	return m
}

func LoadHotels(path string) ([]Hotel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hotels file: %w", err)
	}
	var hotels []Hotel
	if err := json.Unmarshal(data, &hotels); err != nil {
		return nil, fmt.Errorf("parse hotels file: %w", err)
	}
	return hotels, nil
}

// Language is one of the three output languages the pipeline produces.
type Language string

const (
	English Language = "en"
	German  Language = "de"
	French  Language = "fr"
)

var Languages = []Language{English, German, French}

func (l Language) Name() string {
	switch l {
	case English:
		return "English"
	case German:
		return "German"
	case French:
		return "French"
	default:
		return string(l)
	}
}
