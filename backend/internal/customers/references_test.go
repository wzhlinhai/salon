package customers

import (
	"context"
	"slices"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestEveryCustomerReferenceIsMerged(t *testing.T) {
	pool := dbtest.NewPool(t)
	rows, err := pool.Query(context.Background(), `SELECT c.conrelid::regclass::text, a.attname
 FROM pg_constraint c
 JOIN LATERAL unnest(c.conkey,c.confkey) AS k(local_key,remote_key) ON true
 JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=k.local_key
 JOIN pg_attribute target ON target.attrelid=c.confrelid AND target.attnum=k.remote_key
 WHERE c.contype='f' AND c.confrelid='customers'::regclass AND target.attname='id'
 AND NOT (c.conrelid='customers'::regclass AND a.attname='merged_into')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, table+"."+column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// 日志只追加，关联写入时的顾客，不随合并改挂；其余外键仍须全部登记。
	expected := []string{"audit_log.customer_id"}
	for _, ref := range customerReferences {
		expected = append(expected, ref.table+"."+ref.column)
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("customer foreign keys %v differ from merge list %v", actual, expected)
	}
}
