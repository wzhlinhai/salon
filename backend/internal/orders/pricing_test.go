package orders

import (
	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/money"
	"reflect"
	"testing"
	"time"
)

func pricingAmount(s string) money.Amount {
	var a money.Amount
	if err := a.Scan(s); err != nil {
		panic(err)
	}
	return a
}
func TestPriceCheckout(t *testing.T) {
	item, typeID, level := uuid.New(), uuid.New(), uuid.New()
	var percent money.Percent
	if err := percent.Scan("88"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source, price, member, rule, wantPrice, wantSource, wantDiscount, wantNet string
		discountable, card, removed                                                     bool
	}{
		{"no card", "level", "100", "80", "member_first", "100.00", "level", "0.00", "100.00", true, false, false},
		{"manual", "manual", "100", "80", "member_first", "100.00", "manual", "0.00", "100.00", true, true, false},
		{"member first", "level", "100", "120", "member_first", "120.00", "member", "0.00", "120.00", true, true, false},
		{"lowest above", "level", "100", "120", "lowest", "100.00", "level", "12.00", "88.00", true, true, false},
		{"lowest equal", "level", "100", "100", "lowest", "100.00", "member", "0.00", "100.00", true, true, false},
		{"lowest below", "level", "100", "80", "lowest", "80.00", "member", "0.00", "80.00", true, true, false},
		{"discount", "standard", "100", "", "member_first", "100.00", "standard", "12.00", "88.00", true, true, false},
		{"not discountable", "standard", "100", "", "member_first", "100.00", "standard", "0.00", "100.00", false, true, false},
		{"not discountable with member price", "level", "100", "80", "member_first", "100.00", "level", "0.00", "100.00", false, true, false},
		{"half up", "standard", "0.25", "", "member_first", "0.25", "standard", "0.02", "0.23", true, true, false},
		{"removed", "standard", "100", "80", "member_first", "100.00", "standard", "0.00", "0.00", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := percent
			if tc.name == "half up" {
				if err := p.Scan("90"); err != nil {
					t.Fatal(err)
				}
			}
			line := OrderItem{ItemID: item, Price: pricingAmount(tc.price), PriceSource: tc.source}
			if tc.source == "level" {
				line.LevelID = &level
			}
			if tc.removed {
				line.RemovedAt = new(time.Now())
			}
			input := Order{Items: []OrderItem{line}}
			pricing := linePricing{discountable: tc.discountable}
			if tc.member != "" {
				pricing.member = new(pricingAmount(tc.member))
			}
			var card *uuid.UUID
			if tc.card {
				card = &typeID
			}
			got := priceCheckout(input, map[uuid.UUID]linePricing{item: pricing}, card, &p, tc.rule)
			result := got.Items[0]
			if result.Price.String() != tc.wantPrice || result.PriceSource != tc.wantSource || result.Discount.String() != tc.wantDiscount || got.Net != tc.wantNet {
				t.Fatal(got)
			}
			if result.PriceSource == "member" && (result.LevelID != nil || result.CardTypeID == nil || *result.CardTypeID != typeID) {
				t.Fatal(result)
			}
			if !tc.discountable && !reflect.DeepEqual(result, line) {
				t.Fatal("rewrote non-discountable line", result)
			}
			if !reflect.DeepEqual(input.Items[0], line) {
				t.Fatal("modified input")
			}
			if tc.removed && !reflect.DeepEqual(result, line) {
				t.Fatal("rewrote removed line")
			}
		})
	}
}
