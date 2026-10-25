package orders_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestPreviewBatchStoreFilters(t *testing.T) {
	s := open(t)
	ct := s.paymentType("卡", nil)
	card := s.paymentCard(ct, "1000", "0")
	other := s.otherStore()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	q := dbgen.New(tx)
	ctx := context.Background()
	types, err := q.GetCardTypes(ctx, dbgen.GetCardTypesParams{StoreID: other, Ids: []uuid.UUID{id(ct["id"])}})
	if err != nil || len(types) != 0 {
		t.Fatal(types, err)
	}
	items, err := q.GetItems(ctx, dbgen.GetItemsParams{StoreID: other, Ids: []uuid.UUID{s.item}})
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	cards, err := q.CardViews(ctx, dbgen.CardViewsParams{StoreID: other, Ids: []uuid.UUID{id(card["id"])}})
	if err != nil || len(cards) != 0 {
		t.Fatal(cards, err)
	}
	customers, err := q.GetCustomers(ctx, dbgen.GetCustomersParams{StoreID: other, Ids: []uuid.UUID{s.customer}})
	if err != nil || len(customers) != 0 {
		t.Fatal(customers, err)
	}
}
