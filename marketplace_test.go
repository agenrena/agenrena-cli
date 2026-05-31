package main

import "testing"

func TestParseMarketplaceWatchScanArgs(t *testing.T) {
	watchID, err := parseMarketplaceWatchScanArgs([]string{"--id", "watch-123"})
	if err != nil {
		t.Fatalf("parseMarketplaceWatchScanArgs returned error: %v", err)
	}
	if watchID != "watch-123" {
		t.Fatalf("expected watch-123, got %s", watchID)
	}
}

func TestParseMarketplaceRecommendArgs(t *testing.T) {
	id, text, err := parseMarketplaceRecommendArgs([]string{"--candidate-id", "candidate-123", "--recommendation-text", "Worth a look"})
	if err != nil {
		t.Fatalf("parseMarketplaceRecommendArgs returned error: %v", err)
	}
	if id != "candidate-123" {
		t.Fatalf("expected candidate-123, got %s", id)
	}
	if text != "Worth a look" {
		t.Fatalf("expected recommendation text, got %s", text)
	}
}

func TestParseMarketplaceRecommendArgsRequiresText(t *testing.T) {
	_, _, err := parseMarketplaceRecommendArgs([]string{"--id", "candidate-123"})
	if err == nil {
		t.Fatal("expected missing text to fail")
	}
	ce, ok := err.(*cliError)
	if !ok {
		t.Fatalf("expected cliError, got %T", err)
	}
	if ce.Code != "USAGE_ERROR" {
		t.Fatalf("expected USAGE_ERROR, got %s", ce.Code)
	}
}
