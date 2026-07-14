// Command localize generates and scores English/German/French hotel
// descriptions from a shared fact catalog, retrying each language until it
// passes the consistency and nativeness bar (or attempts run out), and
// prints/saves the results.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/rosty-git/test-repo/internal/llm"
	"github.com/rosty-git/test-repo/internal/model"
	"github.com/rosty-git/test-repo/internal/pipeline"
)

func main() {
	hotelsPath := flag.String("hotels", "testdata/hotels.json", "path to the source hotel facts JSON")
	outDir := flag.String("out", "results", "directory to write the run's JSON results file")
	maxAttempts := flag.Int("max-attempts", 0, "override the max retry attempts per language (0 = use default)")
	hotelID := flag.String("hotel-id", "", "only run the hotel with this id (default: run all)")
	flag.Parse()

	if err := run(*hotelsPath, *outDir, *maxAttempts, *hotelID); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(hotelsPath, outDir string, maxAttempts int, hotelID string) error {
	hotels, err := model.LoadHotels(hotelsPath)
	if err != nil {
		return err
	}
	if hotelID != "" {
		var filtered []model.Hotel
		for _, h := range hotels {
			if h.ID == hotelID {
				filtered = append(filtered, h)
			}
		}
		hotels = filtered
	}

	client, err := llm.NewAnthropicClient()
	if err != nil {
		return fmt.Errorf("%w (set ANTHROPIC_API_KEY - see README.md)", err)
	}

	cfg := pipeline.DefaultConfig()
	if maxAttempts > 0 {
		cfg.MaxAttempts = maxAttempts
	}

	ctx := context.Background()
	var results []pipeline.HotelResult
	for _, hotel := range hotels {
		res, err := pipeline.RunHotel(ctx, client, cfg, hotel, func(msg string) { fmt.Println(msg) })
		if err != nil {
			return fmt.Errorf("run hotel %s: %w", hotel.ID, err)
		}
		results = append(results, res)
		printHotelSummary(res)
	}

	printOverallSummary(results)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	outPath := filepath.Join(outDir, fmt.Sprintf("run-%s.json", time.Now().UTC().Format("20060102T150405Z")))
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create results file: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("write results file: %w", err)
	}
	fmt.Printf("\nFull results written to %s\n", outPath)
	return nil
}

func printHotelSummary(res pipeline.HotelResult) {
	fmt.Printf("\n=== %s (%s, %s) ===\n", res.Hotel.Name, res.Hotel.City, res.Hotel.Country)
	fmt.Printf("Selected %d facts for the English base.\n", len(res.FactIDs))
	for _, lang := range model.Languages {
		lr := res.Languages[lang]
		final := lr.Final()
		status := "FAIL"
		if final.Pass {
			status = "PASS"
		}
		fmt.Printf("\n[%s] %s after %d attempt(s) - consistency F1=%.2f (exact=%v) nativeness=%d/5 overall=%.2f\n",
			lang, status, len(lr.Attempts), final.Consistency.F1, final.Consistency.ExactMatch, final.Nativeness.Score, final.OverallScore)
		fmt.Printf("%s\n", final.Text)
	}
}

func printOverallSummary(results []pipeline.HotelResult) {
	fmt.Printf("\n=== Run summary ===\n")
	total, passed := 0, 0
	var sumF1, sumNative float64
	for _, res := range results {
		for _, lang := range model.Languages {
			final := res.Languages[lang].Final()
			total++
			if final.Pass {
				passed++
			}
			sumF1 += final.Consistency.F1
			sumNative += float64(final.Nativeness.Score)
		}
	}
	if total == 0 {
		return
	}
	fmt.Printf("%d/%d language outputs passed the bar (exact factual match + nativeness >= %d/5).\n", passed, total, pipeline.DefaultConfig().NativenessPassBar)
	fmt.Printf("Average consistency F1: %.2f | Average nativeness: %.1f/5\n", sumF1/float64(total), sumNative/float64(total))
}
