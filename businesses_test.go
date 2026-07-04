package main

import "testing"

func TestParseBusinessOfferingSearchArgs(t *testing.T) {
	opts, err := parseBusinessOfferingSearchArgs([]string{
		"--category", "stay",
		"--state-code", "TW-HUA",
		"--city-id", "123",
		"--party-size", "2",
		"--price-max", "5000",
		"--service-period", "morning",
		"--service-period", "evening",
		"--required-tag", "romantic",
		"--preferred-tag", "quiet",
		"--latitude", "23.991100",
		"--longitude", "121.611200",
		"--page", "2",
	})
	if err != nil {
		t.Fatalf("parseBusinessOfferingSearchArgs returned error: %v", err)
	}
	if opts.category != "stay" || opts.stateCode != "TW-HUA" {
		t.Fatalf("unexpected basic options: %#v", opts)
	}
	if opts.cityID == nil || *opts.cityID != 123 {
		t.Fatalf("unexpected city id: %#v", opts.cityID)
	}
	if opts.partySize == nil || *opts.partySize != 2 {
		t.Fatalf("unexpected party size: %#v", opts.partySize)
	}
	if opts.priceMax == nil || *opts.priceMax != 5000 {
		t.Fatalf("unexpected price max: %#v", opts.priceMax)
	}
	if len(opts.servicePeriods) != 2 || len(opts.requiredTags) != 1 || len(opts.preferredTags) != 1 {
		t.Fatalf("unexpected list options: %#v", opts)
	}
	if opts.page != 2 {
		t.Fatalf("unexpected page: %d", opts.page)
	}
	body := opts.requestBody()
	if _, ok := body["tags"]; ok {
		t.Fatal("request body must not contain removed tags field")
	}
	if tags, ok := body["required_tags"].([]string); !ok || len(tags) != 1 || tags[0] != "romantic" {
		t.Fatalf("unexpected required_tags: %#v", body["required_tags"])
	}
	if tags, ok := body["preferred_tags"].([]string); !ok || len(tags) != 1 || tags[0] != "quiet" {
		t.Fatalf("unexpected preferred_tags: %#v", body["preferred_tags"])
	}
}

func TestParseBusinessOfferingSearchArgsValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "category required", args: nil},
		{name: "removed kind option rejected", args: []string{"--category", "stay", "--kind", "single"}},
		{name: "removed tag option rejected", args: []string{"--category", "stay", "--tag", "romantic"}},
		{name: "old state option rejected", args: []string{"--category", "stay", "--state", "TW-HUA"}},
		{name: "coordinates paired", args: []string{"--category", "stay", "--latitude", "23.9"}},
		{name: "party size positive", args: []string{"--category", "stay", "--party-size", "0"}},
		{name: "city id positive", args: []string{"--category", "stay", "--city-id", "0"}},
		{name: "price nonnegative", args: []string{"--category", "stay", "--price-max", "-1"}},
		{name: "page positive", args: []string{"--category", "stay", "--page", "0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseBusinessOfferingSearchArgs(test.args); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseBusinessOfferingSearchOptionsArgs(t *testing.T) {
	opts, err := parseBusinessOfferingSearchOptionsArgs([]string{
		"--country-code", "TW",
		"--state-code", "TW-HUA",
	})
	if err != nil {
		t.Fatalf("parseBusinessOfferingSearchOptionsArgs returned error: %v", err)
	}
	if opts.countryCode != "TW" || opts.stateCode != "TW-HUA" {
		t.Fatalf("unexpected options: %#v", opts)
	}
	if _, err := parseBusinessOfferingSearchOptionsArgs(nil); err == nil {
		t.Fatal("expected missing country code error")
	}
}

func TestParseBusinessOfferingListArgs(t *testing.T) {
	identityID, err := parseBusinessOfferingListArgs([]string{"--identity-id", "business-id"})
	if err != nil {
		t.Fatalf("parseBusinessOfferingListArgs returned error: %v", err)
	}
	if identityID != "business-id" {
		t.Fatalf("unexpected identity id: %q", identityID)
	}
}

func TestEndpointURLPreservesQuery(t *testing.T) {
	client := &APIClient{baseURL: "https://api.example.com/api/agent-api"}
	got := client.endpointURL("/business-offerings/search/?page=2")
	want := "https://api.example.com/api/agent-api/business-offerings/search/?page=2"
	if got != want {
		t.Fatalf("endpointURL() = %q, want %q", got, want)
	}
}
