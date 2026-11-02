package orders

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/cards"
	"github.com/wzhlinhai/salon/backend/internal/money"
)

func planFixture() (checkoutState, checkoutOrder, []uuid.UUID) {
	customer, item := uuid.New(), uuid.New()
	ids := []uuid.UUID{uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002"), uuid.MustParse("00000000-0000-0000-0000-000000000003")}
	state := checkoutState{cards: map[uuid.UUID]cards.CardSnapshot{}, discounts: map[uuid.UUID]*money.Percent{}, lines: map[uuid.UUID]linePricing{item: {discountable: true}}, members: map[uuid.UUID]map[uuid.UUID]money.Amount{}, day: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, id := range ids {
		state.cards[id] = cards.CardSnapshot{ID: id, CustomerID: customer, CardTypeID: uuid.New(), Status: "active", Balance: money.Total{}.AddProduct(pricingAmount("100"), 1)}
	}
	input := checkoutOrder{order: Order{CustomerID: customer, Items: []OrderItem{{ID: uuid.New(), ItemID: item, Price: pricingAmount("100"), PriceSource: "standard"}}}, rules: checkoutRules{cardSelection: "by_balance", memberPriceRule: "lowest"}, candidates: ids, payments: []PaymentInput{{Method: "card", Amount: pricingAmount("100")}}}
	return state, input, ids
}

func planPercent(t *testing.T, value string) *money.Percent {
	t.Helper()
	var percent money.Percent
	if err := percent.Scan(value); err != nil {
		t.Fatal(err)
	}
	return &percent
}

func planError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || err.Error() != message {
		t.Fatalf("want %q, got %v", message, err)
	}
}

func TestCheckoutPlanOrdering(t *testing.T) {
	for _, rule := range []string{"by_balance", "by_discount"} {
		for _, tie := range []string{"primary", "expiry", "unlimited", "opened", "uuid"} {
			t.Run(rule+"/"+tie, func(t *testing.T) {
				state, input, ids := planFixture()
				input.rules.cardSelection = rule
				input.candidates = []uuid.UUID{ids[2], ids[1], ids[0]}
				a, b := state.cards[ids[0]], state.cards[ids[1]]
				want := ids[0]
				switch tie {
				case "primary":
					want = ids[1]
					b.Balance = money.Total{}.AddProduct(pricingAmount("50"), 1)
					state.discounts[b.CardTypeID] = planPercent(t, "50")
				case "expiry":
					a.ExpiresOn = new(state.day.AddDate(0, 0, 1))
					b.ExpiresOn = new(state.day.AddDate(0, 0, 2))
				case "unlimited":
					a.ExpiresOn = new(state.day)
				case "opened":
					a.OpenedAt = state.day.AddDate(0, 0, -2)
					b.OpenedAt = state.day.AddDate(0, 0, -1)
				}
				state.cards[ids[0]], state.cards[ids[1]] = a, b
				input.candidates = input.candidates[1:]
				_, got, err := quoteOrder(state, input, nil, true)
				if err != nil || got == nil || *got != want {
					t.Fatalf("want %v got %v err %v", want, got, err)
				}
			})
		}
	}
}

func TestCheckoutPlanFiltering(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		edit          func(*cards.CardSnapshot, checkoutState)
	}{
		{"holder", "不能用别人的卡", func(c *cards.CardSnapshot, _ checkoutState) { c.CustomerID = uuid.New() }},
		{"refunded", "卡已退", func(c *cards.CardSnapshot, _ checkoutState) { c.Status = "refunded" }},
		{"expired", "卡已过期", func(c *cards.CardSnapshot, s checkoutState) { c.ExpiresOn = new(s.day.AddDate(0, 0, -1)) }},
		{"count", "次卡不能用卡余额支付", func(c *cards.CardSnapshot, _ checkoutState) { c.Kind = cards.CountCard }},
		{"empty", "卡余额不足", func(c *cards.CardSnapshot, _ checkoutState) { c.Balance = money.Total{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, input, ids := planFixture()
			card := state.cards[ids[0]]
			tc.edit(&card, state)
			state.cards[ids[0]] = card
			planError(t, card.CheckSelection(input.order.CustomerID, state.day), tc.message)
			input.candidates = ids[:1]
			_, _, err := quoteOrder(state, input, nil, true)
			planError(t, err, "这位顾客没有能用的卡")
			input.candidates = ids
			_, id, err := quoteOrder(state, input, nil, true)
			if err != nil || id == nil || *id != ids[1] {
				t.Fatalf("%v %v", id, err)
			}
		})
	}
}

func TestCheckoutPlanPricingCard(t *testing.T) {
	for _, mode := range []string{"explicit", "auto", "count first", "auto before explicit"} {
		t.Run(mode, func(t *testing.T) {
			state, input, ids := planFixture()
			want := ids[0]
			switch mode {
			case "explicit":
				input.payments[0].CardID = &ids[1]
				want = ids[1]
			case "count first":
				line := input.order.Items[0]
				line.ID = uuid.New()
				input.order.Items = append(input.order.Items, line)
				input.payments = append([]PaymentInput{{Method: "count", CardID: &ids[2], OrderItemID: &line.ID, Amount: pricingAmount("100")}}, input.payments...)
				input.candidates = ids[:2]
			case "auto before explicit":
				input.payments[0].Amount = pricingAmount("50")
				input.payments = append(input.payments, PaymentInput{Method: "card", CardID: &ids[1], Amount: pricingAmount("50")})
				input.candidates = []uuid.UUID{ids[0], ids[2]}
			}
			got, err := planCheckout(state, []checkoutOrder{input})
			if err != nil {
				t.Fatal(err)
			}
			if got[0].pricingTypeID == nil || *got[0].pricingTypeID != state.cards[want].CardTypeID {
				t.Fatal(got)
			}
		})
	}
}

func TestCheckoutPlanBalances(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "auto", true: "explicit"}[explicit], func(t *testing.T) {
			state, input, ids := planFixture()
			first := input
			if explicit {
				first.payments = []PaymentInput{{Method: "card", CardID: &ids[0], Amount: pricingAmount("100")}}
			}
			got, err := planCheckout(state, []checkoutOrder{first, input})
			if err != nil {
				t.Fatal(err)
			}
			if *got[1].pricingTypeID != state.cards[ids[1]].CardTypeID || *got[1].payments[0].CardID != ids[1] {
				t.Fatal(got)
			}
			if state.cards[ids[0]].Balance.Format() != "100.00" {
				t.Fatal("modified snapshot")
			}
		})
	}
	for _, tc := range []struct {
		name, amount, message string
		want                  []string
	}{
		{"split", "150", "", []string{"100.00", "50.00"}},
		{"short", "301", "卡余额不足", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, input, ids := planFixture()
			input.order.Items[0].Price = pricingAmount(tc.amount)
			input.payments = []PaymentInput{{Method: "cash", Amount: pricingAmount("1")}, {Method: "card", Amount: pricingAmount(tc.amount)}, {Method: "cash", Amount: pricingAmount("1")}}
			input.order.Items = append(input.order.Items, OrderItem{ID: uuid.New(), Price: pricingAmount("2"), PriceSource: "manual"})
			got, err := planCheckout(state, []checkoutOrder{input})
			if tc.message != "" {
				planError(t, err, tc.message)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			payments := got[0].payments
			if len(payments) != 4 || payments[0].Method != "cash" || payments[3].Method != "cash" {
				t.Fatal(payments)
			}
			for i, want := range tc.want {
				if payments[i+1].Amount.String() != want || *payments[i+1].CardID != ids[i] {
					t.Fatal(payments)
				}
			}
		})
	}
	state, input, ids := planFixture()
	input.payments = []PaymentInput{{Method: "card", CardID: &ids[0], Amount: pricingAmount("100")}}
	_, err := planCheckout(state, []checkoutOrder{input, input})
	planError(t, err, "卡余额不足")
}

func TestCheckoutPlanChangedLines(t *testing.T) {
	for _, mode := range []string{"unchanged", "price", "source", "level", "type", "discount", "removed"} {
		t.Run(mode, func(t *testing.T) {
			_, input, _ := planFixture()
			before := input.order
			priced := before
			priced.Items = append([]OrderItem{}, before.Items...)
			line := &priced.Items[0]
			switch mode {
			case "price":
				line.Price = pricingAmount("80")
			case "source":
				line.PriceSource = "member"
			case "level":
				line.LevelID = new(uuid.New())
			case "type":
				line.CardTypeID = new(uuid.New())
			case "discount":
				line.Discount = pricingAmount("20")
			case "removed":
				line.RemovedAt = new(time.Now())
				line.Price = pricingAmount("80")
			}
			got := changedCheckoutLines(before, priced)
			if mode == "unchanged" || mode == "removed" {
				if len(got) != 0 {
					t.Fatal(got)
				}
				return
			}
			want := []checkoutLine{{ID: line.ID, Price: line.Price, PriceSource: line.PriceSource, LevelID: line.LevelID, CardTypeID: line.CardTypeID, Discount: line.Discount}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%v != %v", got, want)
			}
		})
	}
	state, input, ids := planFixture()
	state.discounts[state.cards[ids[0]].CardTypeID] = planPercent(t, "80")
	input.payments[0].Amount = pricingAmount("80")
	got, err := planCheckout(state, []checkoutOrder{input})
	if err != nil || len(got[0].changedLines) != 1 || got[0].changedLines[0].Discount.String() != "20.00" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestCheckoutPlanRules(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		edit          func(*checkoutOrder, []uuid.UUID)
	}{
		{"empty", "单上没有项目", func(in *checkoutOrder, _ []uuid.UUID) { in.order.Items = nil }},
		{"removed", "单上没有项目", func(in *checkoutOrder, _ []uuid.UUID) { in.order.Items[0].RemovedAt = new(time.Now()) }},
		{"position", "必填岗位还没填人", func(in *checkoutOrder, _ []uuid.UUID) {
			in.order.Items[0].Positions = []OrderItemPosition{{Required: true}}
		}},
		{"zero", "支付金额须大于零", func(in *checkoutOrder, _ []uuid.UUID) { in.payments[0].Amount = money.Amount{} }},
		{"total", "支付合计须等于实收", func(in *checkoutOrder, _ []uuid.UUID) { in.payments[0].Amount = pricingAmount("1") }},
		{"count amount", "扣次金额须等于这一行的实收", func(in *checkoutOrder, ids []uuid.UUID) {
			in.payments = []PaymentInput{{Method: "count", CardID: &ids[0], OrderItemID: &in.order.Items[0].ID, Amount: pricingAmount("1")}}
		}},
		{"count line", "扣次的项目行不在这张单上", func(in *checkoutOrder, ids []uuid.UUID) {
			in.payments = []PaymentInput{{Method: "count", CardID: &ids[0], OrderItemID: new(uuid.New()), Amount: pricingAmount("100")}}
		}},
		{"count removed", "扣次的项目行不在这张单上", func(in *checkoutOrder, ids []uuid.UUID) {
			in.order.Items[0].RemovedAt = new(time.Now())
			in.payments = []PaymentInput{{Method: "count", CardID: &ids[0], OrderItemID: &in.order.Items[0].ID, Amount: pricingAmount("100")}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, input, ids := planFixture()
			tc.edit(&input, ids)
			_, err := planCheckout(state, []checkoutOrder{input})
			planError(t, err, tc.message)
		})
	}
}

func TestCheckoutPlanQuote(t *testing.T) {
	for _, mode := range []string{"explicit", "auto", "no card", "other holder", "count", "allowed"} {
		t.Run(mode, func(t *testing.T) {
			state, input, ids := planFixture()
			input.order.Items[0].Positions = []OrderItemPosition{{Required: true}}
			state.discounts[state.cards[ids[0]].CardTypeID] = planPercent(t, "80")
			var explicit *uuid.UUID
			auto := mode == "auto"
			message := ""
			if mode != "auto" && mode != "no card" {
				explicit = &ids[0]
			}
			if mode == "other holder" || mode == "allowed" {
				card := state.cards[ids[0]]
				card.CustomerID = uuid.New()
				card.Kind = cards.CountCard
				state.cards[ids[0]] = card
				message = "不能用别人的卡"
			}
			if mode == "count" || mode == "allowed" {
				card := state.cards[ids[0]]
				card.Kind = cards.CountCard
				state.cards[ids[0]] = card
				message = "次卡不能用卡余额支付"
				input.rules.othersCardPayment = "allowed"
			}
			got, id, err := quoteOrder(state, input, explicit, auto)
			if message != "" {
				planError(t, err, message)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "80.00"
			if mode == "no card" {
				want = "100.00"
				if id != nil {
					t.Fatal(id)
				}
			} else if id == nil || *id != ids[0] {
				t.Fatal(id)
			}
			if got.Net != want || len(got.Payments) != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestCheckoutPlanExplicitChecks(t *testing.T) {
	for _, tc := range []struct {
		method      string
		otherHolder bool
		message     string
	}{
		{"card", true, "不能用别人的卡"},
		{"card", false, "次卡不能用卡余额支付"},
		{"count", true, "不能用别人的卡"},
		{"count", false, ""},
	} {
		t.Run(tc.method+"/"+tc.message, func(t *testing.T) {
			state, input, ids := planFixture()
			card := state.cards[ids[0]]
			card.Kind = cards.CountCard
			if tc.otherHolder {
				card.CustomerID = uuid.New()
			}
			state.cards[ids[0]] = card
			input.payments[0].Method = tc.method
			input.payments[0].CardID = &ids[0]
			if tc.method == "count" {
				input.payments[0].OrderItemID = &input.order.Items[0].ID
			}
			_, err := planCheckout(state, []checkoutOrder{input})
			if tc.message != "" {
				planError(t, err, tc.message)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
