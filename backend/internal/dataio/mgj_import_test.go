package dataio

import (
	"os"
	"testing"
)

func TestParseMGJSampleWorkbook(t *testing.T) {
	file, err := os.Open("testdata/mgj-sample.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	parsed, err := parseWorkbook(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Customers) != 6 || len(parsed.Stored) != 6 || len(parsed.Counts) != 5 {
		t.Fatalf("rows = customers %d, stored %d, counts %d", len(parsed.Customers), len(parsed.Stored), len(parsed.Counts))
	}
	if parsed.Summary.Customers != 6 || parsed.Summary.StoredCards != 6 || parsed.Summary.CountPackageCards != 5 {
		t.Fatalf("summary = %+v", parsed.Summary)
	}
	if parsed.Summary.Principal.String() != "7070.10" || parsed.Summary.Bonus.String() != "250.00" {
		t.Fatalf("summary totals = principal %s, bonus %s", parsed.Summary.Principal, parsed.Summary.Bonus)
	}

	normalized, badPhone := false, false
	for _, customer := range parsed.Customers {
		switch customer.SourceKey {
		case "C001":
			normalized = customer.Phone != nil && *customer.Phone == "13800131234"
		case "C002":
			badPhone = customer.Phone == nil
		}
	}
	if !normalized || !badPhone {
		t.Fatalf("phones = normalized %v, bad blank %v", normalized, badPhone)
	}
	for _, card := range parsed.Stored {
		if card.SourceKey == "K002" && card.Principal.String() != "5569.60" {
			t.Fatalf("K002 principal = %s", card.Principal)
		}
	}
}
