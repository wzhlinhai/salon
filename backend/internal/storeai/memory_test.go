package storeai_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

func writeMemory(t *testing.T, shop *optest.Shop, inboxID uuid.UUID, path, content string) {
	t.Helper()
	if err := writeMemoryErr(shop, inboxID, path, content, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func writeMemoryErr(shop *optest.Shop, inboxID uuid.UUID, path, content string, now time.Time) error {
	return db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		return storeai.WriteMemory(context.Background(), q, now, inboxID, path, content)
	})
}

func readMemory(t *testing.T, shop *optest.Shop, inboxID uuid.UUID, path string) string {
	t.Helper()
	var content string
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		content, err = storeai.ReadMemory(context.Background(), q, inboxID, path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestMemoryAllowedPaths(t *testing.T) {
	shop := open(t)
	inbox := storeInbox(t, shop)
	allowed := []string{"instructions.md", "index.md", "reported.md", "notes/a.md", "progress/b_1.md"}
	for _, path := range allowed {
		writeMemory(t, shop, inbox.ID, path, "内容 "+path)
		if got := readMemory(t, shop, inbox.ID, path); got != "内容 "+path {
			t.Fatalf("%s = %q", path, got)
		}
	}
	invalid := []string{"../x.md", "notes/a/b.md", "foo.md", "Notes/x.md"}
	for _, path := range invalid {
		err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
			return storeai.WriteMemory(context.Background(), q, time.Now(), inbox.ID, path, "x")
		})
		if !errors.Is(err, storeai.ErrInvalidMemoryPath) {
			t.Fatalf("%s = %v, want ErrInvalidMemoryPath", path, err)
		}
	}
}

func TestMemorySizeAndFileLimit(t *testing.T) {
	shop := open(t)
	inbox := staffInbox(t, shop, shop.Res.OwnerID)
	err := writeMemoryErr(shop, inbox.ID, "notes/too-big.md", strings.Repeat("a", storeai.MaxMemoryFileBytes+1), time.Now())
	if !errors.Is(err, storeai.ErrMemoryTooLarge) {
		t.Fatalf("too large = %v", err)
	}
	err = db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		for i := range storeai.MaxMemoryFiles {
			if err := storeai.WriteMemory(context.Background(), q, time.Now(), inbox.ID, fmt.Sprintf("notes/n%03d.md", i), "x"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = writeMemoryErr(shop, inbox.ID, "notes/over.md", "x", time.Now())
	if !errors.Is(err, storeai.ErrMemoryLimit) {
		t.Fatalf("201st file = %v", err)
	}
}

func TestMemoryFileLimitLock(t *testing.T) {
	shop := open(t)
	inbox := staffInbox(t, shop, shop.Res.OwnerID)
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		for i := range storeai.MaxMemoryFiles - 1 {
			if err := storeai.WriteMemory(context.Background(), q, time.Now(), inbox.ID, fmt.Sprintf("notes/n%03d.md", i), "x"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	lock := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	dbtest.Exec(t, lock, "SELECT 1 FROM ai_inboxes WHERE id=$1 FOR NO KEY UPDATE", inbox.ID)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- writeMemoryErr(shop, inbox.ID, fmt.Sprintf("notes/race%d.md", i), "x", time.Now())
		}(i)
	}
	waitLockWaiters(t, shop, 2)
	dbtest.Rollback(t, lock)
	wg.Wait()
	close(results)
	success, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, storeai.ErrMemoryLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || limited != 1 {
		t.Fatalf("success=%d limited=%d", success, limited)
	}
}

func TestMemoryIsolationAndAlwaysMemory(t *testing.T) {
	shop := open(t)
	store := storeInbox(t, shop)
	staff := staffInbox(t, shop, shop.Res.OwnerID)
	writeMemory(t, shop, store.ID, "instructions.md", "门店规矩")
	writeMemory(t, shop, staff.ID, "instructions.md", "员工自己的")
	if got := readMemory(t, shop, store.ID, "instructions.md"); got != "门店规矩" {
		t.Fatalf("store memory = %q", got)
	}
	if got := readMemory(t, shop, staff.ID, "instructions.md"); got != "员工自己的" {
		t.Fatalf("staff memory = %q", got)
	}
	writeMemory(t, shop, staff.ID, "index.md", "索引")
	writeMemory(t, shop, staff.ID, "reported.md", "已报")
	writeMemory(t, shop, staff.ID, "notes/private.md", "按需")
	writeMemory(t, shop, staff.ID, "progress/current.md", "进行中")
	var memory storeai.AlwaysMemory
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		memory, err = storeai.LoadAlwaysMemory(context.Background(), q, staff.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if memory.Instructions != "员工自己的" || memory.Index != "索引" || memory.Reported != "已报" {
		t.Fatalf("always memory = %+v", memory)
	}
	files, err := listMemory(shop, staff.ID)
	if err != nil {
		t.Fatal(err)
	}
	bytesByPath := map[string]int{}
	for _, file := range files {
		bytesByPath[file.Path] = file.Bytes
	}
	if len(files) != 5 || bytesByPath["progress/current.md"] != len("进行中") || bytesByPath["notes/private.md"] != len("按需") {
		t.Fatalf("files = %+v", files)
	}
}

func listMemory(shop *optest.Shop, inboxID uuid.UUID) ([]storeai.MemoryFile, error) {
	var files []storeai.MemoryFile
	err := db.InTenant(context.Background(), shop.Pool, shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		files, err = storeai.ListMemory(context.Background(), q, inboxID)
		return err
	})
	return files, err
}
