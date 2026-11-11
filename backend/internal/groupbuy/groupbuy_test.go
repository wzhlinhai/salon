package groupbuy_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

var instant = time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*optest.Shop, *fakeprovider.Fake, ops.Caller) {
	t.Helper()
	s := optest.OpenShop(t)
	s.SetNow(instant)
	f := fakeprovider.New("douyin", instant)
	s.Groupbuy.Register("douyin", f)
	return s, f, s.MustCaller(s.Owner)
}
func request(key string) groupbuy.CouponRequest {
	return groupbuy.CouponRequest{Platform: "douyin", CouponCode: key, Key: key}
}
func success(t *testing.T, r optest.Result, status string) {
	t.Helper()
	if r.Status != 200 || r.Body["status"] != status {
		t.Fatalf("want %s: %+v", status, r)
	}
}
func rejection(t *testing.T, r optest.Result, status int, detail string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Detail(), detail) {
		t.Fatalf("want %d %s: %+v", status, detail, r)
	}
}
func auditCount(t *testing.T, s *optest.Shop, action string, want int) {
	t.Helper()
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var n int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM audit_log WHERE action=$1 AND target_kind='coupon_redemption' AND customer_id IS NULL", action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("%s audit count %d want %d", action, n, want)
	}
}

func TestUnknown(t *testing.T) {
	for _, applied := range []bool{true, false} {
		t.Run(map[bool]string{true: "applied", false: "still unknown"}[applied], func(t *testing.T) {
			s, f, c := setup(t)
			in := request("timeout")
			f.Replies["redeem"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "redeemed", Platform: "douyin", RedeemedAt: &instant}, Err: groupbuy.ErrUnknown, Apply: applied}}
			rejection(t, s.HTTP(t, &c, "redeem_coupon", in), 409, "结果未知，请稍后查询")
			for _, ch := range s.Channels() {
				success(t, ch.Call(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "unknown")
			}
			if !applied {
				f.Replies["query"] = []fakeprovider.Reply{{Err: groupbuy.ErrUnknown}}
			}
			r := s.HTTP(t, &c, "redeem_coupon", in)
			if applied {
				success(t, r, "redeemed")
				auditCount(t, s, "redeem_coupon", 1)
			} else {
				rejection(t, r, 409, "结果未知，请稍后查询")
			}
			if f.Count("redeem") != 1 || f.Count("query") != 1 {
				t.Fatalf("redeem=%d query=%d", f.Count("redeem"), f.Count("query"))
			}
			if !applied {
				success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
				if f.Count("redeem") != 2 {
					t.Fatal("unused query must allow same-key retry")
				}
			}
			success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
			want := 1
			if !applied {
				want = 2
			}
			if f.Count("redeem") != want {
				t.Fatal("duplicate redeem")
			}
			auditCount(t, s, "get_redemption", 2)
		})
	}
}

func TestRedeemedIdempotent(t *testing.T) {
	s, f, c := setup(t)
	in := request("already redeemed")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	if f.Count("redeem") != 1 || f.Count("query") != 0 {
		t.Fatalf("redeem=%d query=%d", f.Count("redeem"), f.Count("query"))
	}
	auditCount(t, s, "redeem_coupon", 2)
}

func TestRevokeWindow(t *testing.T) {
	for _, tc := range []struct {
		platform string
		minutes  int
		allowed  bool
	}{{"douyin", 59, true}, {"douyin", 60, true}, {"douyin", 61, false}, {"meituan", 61, true}} {
		t.Run(tc.platform+time.Duration(tc.minutes).String(), func(t *testing.T) {
			s, f, c := setup(t)
			f.Platform = tc.platform
			s.Groupbuy.Register(tc.platform, f)
			in := request("window")
			in.Platform = tc.platform
			success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
			s.Advance(time.Duration(tc.minutes) * time.Minute)
			f.Before = func(action string, r groupbuy.CouponRequest) {
				if r.TenantID != c.TenantID || r.StoreID != c.StoreID {
					t.Error("missing provider tenant/store scope")
				}
				if (action == "query" || action == "revoke") && r.Key != "revoke:"+in.Key {
					t.Errorf("revoke key %q", r.Key)
				}
			}
			r := s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key})
			if tc.allowed {
				success(t, r, "revoked")
				success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), "revoked")
				if f.Count("revoke") != 1 || f.Count("query") != 1 {
					t.Fatal("duplicate revoke")
				}
				auditCount(t, s, "revoke_coupon", 2)
			} else {
				rejection(t, r, 409, "抖音核销超过一小时不可撤销")
				if f.Count("revoke") != 0 {
					t.Fatal("rejected revoke called provider")
				}
				success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "redeemed")
			}
		})
	}
}

func TestRevokeRecoveryBeyondWindow(t *testing.T) {
	s, f, c := setup(t)
	in := request("revoke recovery")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	s.Advance(59 * time.Minute)
	f.Replies["revoke"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "revoked", Platform: "douyin", RedeemedAt: &instant}, Err: groupbuy.ErrUnknown, Apply: true}}
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), 409, "结果未知，请稍后查询")
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "redeemed")
	s.Advance(2 * time.Minute)
	success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), "revoked")
	if f.Count("revoke") != 1 || f.Count("query") != 2 {
		t.Fatalf("revoke=%d query=%d", f.Count("revoke"), f.Count("query"))
	}
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "revoked")
}

func TestRevokeWindowBeforeProviderRevoke(t *testing.T) {
	s, f, c := setup(t)
	in := request("window before revoke")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	s.Advance(61 * time.Minute)
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), 409, "一小时")
	if f.Count("query") != 1 || f.Count("revoke") != 0 {
		t.Fatalf("query=%d revoke=%d", f.Count("query"), f.Count("revoke"))
	}
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "redeemed")
}

func TestRevokeQueryPlatformMismatch(t *testing.T) {
	s, f, c := setup(t)
	in := request("revoke platform mismatch")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	f.Replies["query"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "redeemed", Platform: "meituan", RedeemedAt: &instant}}}
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), 409, "结果未知，请稍后查询")
	if f.Count("revoke") != 0 {
		t.Fatalf("revoke calls %d", f.Count("revoke"))
	}
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "redeemed")
}

func TestRedeemReplayWithoutProvider(t *testing.T) {
	s, f, c := setup(t)
	in := request("replay without provider")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	s.Groupbuy.Register("douyin", nil)
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	if f.Count("redeem") != 1 || f.Count("query") != 0 {
		t.Fatalf("redeem=%d query=%d", f.Count("redeem"), f.Count("query"))
	}
}

func TestQueryErrorsAreUnknown(t *testing.T) {
	t.Run("redeem", func(t *testing.T) {
		s, f, c := setup(t)
		in := request("query error redeem")
		f.Replies["redeem"] = []fakeprovider.Reply{{Err: groupbuy.ErrUnknown}}
		rejection(t, s.HTTP(t, &c, "redeem_coupon", in), 409, "结果未知，请稍后查询")
		f.Replies["query"] = []fakeprovider.Reply{{Err: errors.New("network down")}}
		rejection(t, s.HTTP(t, &c, "redeem_coupon", in), 409, "结果未知，请稍后查询")
		for _, ch := range s.Channels() {
			success(t, ch.Call(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "unknown")
		}
		success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
		if f.Count("redeem") != 2 {
			t.Fatalf("redeem calls %d", f.Count("redeem"))
		}
	})
	t.Run("revoke", func(t *testing.T) {
		s, f, c := setup(t)
		in := request("query error revoke")
		success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
		f.Replies["query"] = []fakeprovider.Reply{{Err: errors.New("network down")}}
		rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), 409, "结果未知，请稍后查询")
		success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "redeemed")
		success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), "revoked")
		if f.Count("revoke") != 1 {
			t.Fatalf("revoke calls %d", f.Count("revoke"))
		}
	})
}

func TestRevokeUnconfigured(t *testing.T) {
	s, f, c := setup(t)
	in := request("revoke unconfigured")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	s.Groupbuy.Register("douyin", nil)
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), 409, "团购平台未配置")
	if f.Count("query") != 0 || f.Count("revoke") != 0 {
		t.Fatalf("query=%d revoke=%d", f.Count("query"), f.Count("revoke"))
	}
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "redeemed")
}

// alternate 是独立实现，证明业务操作不依赖 fake 的具体类型。
type alternate struct{ now time.Time }

func (a alternate) Verify(_ context.Context, r groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return groupbuy.CouponResult{Platform: r.Platform, Status: "unused"}, nil
}
func (a alternate) Redeem(_ context.Context, r groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return groupbuy.CouponResult{Platform: r.Platform, Status: "redeemed", RedeemedAt: &a.now}, nil
}
func (a alternate) Revoke(_ context.Context, r groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return groupbuy.CouponResult{Platform: r.Platform, Status: "revoked", RedeemedAt: &a.now}, nil
}
func (a alternate) Query(ctx context.Context, r groupbuy.CouponRequest) (groupbuy.CouponResult, error) {
	return a.Redeem(ctx, r)
}

func TestProviderReplacement(t *testing.T) {
	s, f, c := setup(t)
	for _, p := range []groupbuy.Provider{f, alternate{instant}} {
		s.Groupbuy.Register("douyin", p)
		key := time.Duration(f.Count("redeem") + 1).String()
		success(t, s.HTTP(t, &c, "verify_coupon", request(key)), "unused")
		success(t, s.HTTP(t, &c, "redeem_coupon", request(key)), "redeemed")
		success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: key}), "revoked")
	}
	auditCount(t, s, "verify_coupon", 2)
}

func TestVerifyOutsideTransaction(t *testing.T) {
	s, f, c := setup(t)
	baseline := s.Pool.Stat().AcquiredConns()
	called := false
	f.Before = func(action string, _ groupbuy.CouponRequest) {
		if action != "verify" {
			return
		}
		called = true
		if n := s.Pool.Stat().AcquiredConns(); n > baseline {
			t.Errorf("verify held %d database connections during provider call", n-baseline)
		}
	}
	success(t, s.HTTP(t, &c, "verify_coupon", request("outside transaction")), "unused")
	if !called {
		t.Fatal("provider was not called")
	}
}

func TestAccessAndRules(t *testing.T) {
	s, f, c := setup(t)
	for _, name := range []string{"verify_coupon", "redeem_coupon", "revoke_coupon"} {
		var args any = request("ai")
		if name == "revoke_coupon" {
			args = groupbuy.LookupInput{Key: "ai"}
		}
		r := s.AI(t, c, name, args)
		if !errors.Is(r.Err, ops.ErrUnknownOperation) {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	if f.Count("verify")+f.Count("redeem")+f.Count("revoke") != 0 {
		t.Fatal("AI invoked provider")
	}
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var rows int
	if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM coupon_redemptions").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("AI changed redemption data")
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tool := range s.Registry.Tools(c) {
		if tool.Name == "verify_coupon" || tool.Name == "redeem_coupon" || tool.Name == "revoke_coupon" {
			t.Fatal("AI tool exposed:", tool.Name)
		}
	}
	noPerm := c
	noPerm.Permissions = nil
	for _, ch := range s.Channels() {
		rejection(t, ch.Call(t, noPerm, "get_redemption", groupbuy.LookupInput{Key: "missing"}), 403, "permission denied")
		rejection(t, ch.Call(t, c, "get_redemption", groupbuy.LookupInput{Key: "missing"}), 404, "not found")
	}
	for _, name := range []string{"verify_coupon", "redeem_coupon", "revoke_coupon"} {
		var args any = request("no permission")
		if name == "revoke_coupon" {
			args = groupbuy.LookupInput{Key: "no permission"}
		}
		rejection(t, s.HTTP(t, &noPerm, name, args), 403, "permission denied")
	}
	other := request("not configured")
	other.Platform = "other"
	for _, name := range []string{"verify_coupon", "redeem_coupon"} {
		rejection(t, s.HTTP(t, &c, name, other), 409, "团购平台未配置")
	}
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: "missing"}), 404, "not found")
	success(t, s.HTTP(t, &c, "redeem_coupon", request("bound")), "redeemed")
	other = request("bound")
	other.CouponCode = "different"
	rejection(t, s.HTTP(t, &c, "redeem_coupon", other), 409, "幂等键已用于其他券")
	success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: "bound"}), "revoked")
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("bound")), 409, "当前状态不可核销")
	f.Replies["redeem"] = []fakeprovider.Reply{{Err: groupbuy.ErrUnknown}}
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("pending")), 409, "结果未知，请稍后查询")
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: "pending"}), 409, "只允许撤销已核销的券")
	f.Replies["verify"] = []fakeprovider.Reply{{Err: errors.New("network rejected")}}
	rejection(t, s.HTTP(t, &c, "verify_coupon", request("error")), 409, "团购平台操作失败")
	f.Replies["redeem"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "invalid", Platform: "douyin"}}}
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("invalid")), 409, "平台券状态不允许此操作")
	f.Replies["redeem"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "redeemed", Platform: "douyin"}}}
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("missing time")), 409, "结果未知，请稍后查询")
}

func TestTransactionsAndIsolation(t *testing.T) {
	s, f, c := setup(t)
	ctx := context.Background()
	in := request("transaction")
	f.Before = func(action string, _ groupbuy.CouponRequest) {
		if action != "redeem" {
			return
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var status string
		if err := tx.QueryRow(deadline, "SELECT status FROM coupon_redemptions WHERE idempotency_key=$1 FOR NO KEY UPDATE", in.Key).Scan(&status); err != nil {
			t.Fatalf("pending must be committed without lock: %v", err)
		}
		if status != "pending" {
			t.Fatalf("before external call: %s", status)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	_, err := tx.Exec(ctx, "INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,created_at) VALUES ($1,$2,'douyin','other',$3,'pending',$4,$5)", s.Res.TenantID, s.Res.StoreID, in.Key, s.Res.OwnerID, instant)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("unique key: %v", err)
	}
	_ = tx.Rollback(ctx)
	// 本店过滤必须连同租户 RLS 生效。
	foreign := c
	foreign.StoreID = s.Res.OwnerID
	for _, ch := range s.Channels() {
		rejection(t, ch.Call(t, foreign, "get_redemption", groupbuy.LookupInput{Key: in.Key}), 404, "not found")
	}
	rejection(t, s.HTTP(t, &foreign, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), 404, "not found")
	// revoke_coupon 的外部查询与撤销同样必须在事务和行锁之外。
	f.Before = nil
	revokeIn := request("transaction revoke")
	success(t, s.HTTP(t, &c, "redeem_coupon", revokeIn), "redeemed")
	f.Before = func(action string, _ groupbuy.CouponRequest) {
		if action != "query" && action != "revoke" {
			return
		}
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var status string
		if err := tx.QueryRow(deadline, "SELECT status FROM coupon_redemptions WHERE idempotency_key=$1 FOR NO KEY UPDATE", revokeIn.Key).Scan(&status); err != nil {
			t.Fatalf("revoke %s held the row lock: %v", action, err)
		}
		if status != "redeemed" {
			t.Fatalf("before revoke %s: %s", action, status)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: revokeIn.Key}), "revoked")
	lock := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	if _, err := lock.Exec(ctx, "SELECT id FROM coupon_redemptions WHERE idempotency_key=$1 FOR NO KEY UPDATE", in.Key); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	err = db.InTenant(deadline, s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := q.LockRedemption(deadline, dbgen.LockRedemptionParams{StoreID: s.Res.StoreID, IdempotencyKey: in.Key})
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("row lock must block: %v", err)
	}
}

func TestLateResponses(t *testing.T) {
	for _, status := range []string{"redeemed", "revoked"} {
		t.Run(status, func(t *testing.T) {
			s, f, c := setup(t)
			f.Replies["redeem"] = []fakeprovider.Reply{{Err: groupbuy.ErrUnknown}}
			f.Before = func(action string, r groupbuy.CouponRequest) {
				if action != "redeem" {
					return
				}
				err := db.InTenant(context.Background(), s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
					row, err := q.LockRedemption(context.Background(), dbgen.LockRedemptionParams{StoreID: s.Res.StoreID, IdempotencyKey: r.Key})
					if err != nil {
						return err
					}
					row.RedeemedAt = pgtype.Timestamptz{Time: instant, Valid: true}
					_, err = q.UpdateRedemption(context.Background(), dbgen.UpdateRedemptionParams{StoreID: s.Res.StoreID, IdempotencyKey: r.Key, Status: status, RedeemedAt: row.RedeemedAt, RevokedAt: row.RevokedAt})
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			success(t, s.HTTP(t, &c, "redeem_coupon", request("late")), status)
			for _, ch := range s.Channels() {
				success(t, ch.Call(t, c, "get_redemption", groupbuy.LookupInput{Key: "late"}), status)
			}
		})
	}
}

func TestPlatformFailuresAndRevokeRecovery(t *testing.T) {
	s, f, c := setup(t)
	f.Replies["redeem"] = []fakeprovider.Reply{{Err: errors.New("declined")}}
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("failed")), 409, "团购平台操作失败")
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: "failed"}), "failed")
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("failed")), 409, "当前状态不可核销")
	success(t, s.HTTP(t, &c, "redeem_coupon", request("revoke timeout")), "redeemed")
	f.Replies["revoke"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "revoked", Platform: "douyin", RedeemedAt: &instant}, Err: groupbuy.ErrUnknown, Apply: true}}
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: "revoke timeout"}), 409, "结果未知，请稍后查询")
	success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: "revoke timeout"}), "revoked")
	if f.Count("revoke") != 1 {
		t.Fatal("unknown revoke was repeated")
	}
	// 失配的平台响应也必须保持未知，不能记录成核销成功。
	f.Replies["redeem"] = []fakeprovider.Reply{{Result: groupbuy.CouponResult{Status: "redeemed", Platform: "meituan", RedeemedAt: &instant}}}
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("wrong platform")), 409, "结果未知，请稍后查询")
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: "wrong platform"}), "unknown")
}

func TestFakeIdempotency(t *testing.T) {
	f := fakeprovider.New("douyin", instant)
	ctx := context.Background()
	in := request("fake")
	first, err := f.Redeem(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.Redeem(ctx, in)
	if err != nil || first.Status != second.Status || !first.RedeemedAt.Equal(*second.RedeemedAt) {
		t.Fatalf("duplicate key changed result: %+v %v", second, err)
	}
	in.Key = "revoke:" + in.Key
	for range 2 {
		result, err := f.Revoke(ctx, in)
		if err != nil || result.Status != "revoked" {
			t.Fatalf("revoke: %+v %v", result, err)
		}
	}
}

func TestSchema(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	for _, tc := range []struct{ name, update, code string }{
		{"status", "status='nonsense'", "23514"},
		{"staff FK", "redeemed_by='00000000-0000-0000-0000-000000000000'", "23503"},
		{"order FK", "order_id='00000000-0000-0000-0000-000000000000',deal_id='00000000-0000-0000-0000-000000000000',purchase_price=1,store_price=1,linked_at=clock_timestamp()", "23503"},
		{"store FK", "store_id='00000000-0000-0000-0000-000000000000'", "23503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
			_, err := tx.Exec(ctx, "INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,created_at) VALUES ($1,$2,'douyin','schema',$5,'pending',$3,$4)", s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, instant, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			// 测试约束使用数据库属主更新，避免列级授权提前拒绝而掩盖约束。
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			_, err = s.Pool.Exec(ctx, "UPDATE coupon_redemptions SET "+tc.update)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if _, err := s.Pool.Exec(ctx, "DELETE FROM coupon_redemptions"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInputBounds(t *testing.T) {
	s, _, c := setup(t)
	for _, tc := range []struct {
		field string
		max   int
	}{{"platform", 100}, {"coupon_code", 500}, {"key", 200}} {
		for _, value := range []string{"", strings.Repeat("x", tc.max+1)} {
			in := map[string]string{"platform": "douyin", "coupon_code": "bounds", "key": "bounds"}
			in[tc.field] = value
			r := s.HTTP(t, &c, "verify_coupon", in)
			rejection(t, r, 422, "validation failed")
		}
	}
	for _, value := range []string{"", strings.Repeat("x", 201)} {
		rejection(t, s.HTTP(t, &c, "get_redemption", groupbuy.LookupInput{Key: value}), 422, "validation failed")
	}
}

func TestTenantPolicyAndColumnGrants(t *testing.T) {
	s, _, c := setup(t)
	ctx := context.Background()
	success(t, s.HTTP(t, &c, "redeem_coupon", request("isolated")), "redeemed")
	tx := dbtest.AppTx(t, s.Pool, uuid.New())
	var n int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM coupon_redemptions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("other tenant can read coupons")
	}
	_, err := tx.Exec(ctx, "INSERT INTO coupon_redemptions (tenant_id,store_id,platform,coupon_code,idempotency_key,status,redeemed_by,created_at) VALUES ($1,$2,'douyin','cross','cross','pending',$3,$4)", s.Res.TenantID, s.Res.StoreID, s.Res.OwnerID, instant)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("tenant write policy: %v", err)
	}
	_ = tx.Rollback(ctx)
	for _, sql := range []string{"UPDATE coupon_redemptions SET coupon_code='changed'", "UPDATE coupon_redemptions SET created_at=clock_timestamp()", "UPDATE coupon_redemptions SET idempotency_key='changed'", "DELETE FROM coupon_redemptions"} {
		tx := dbtest.AppTx(t, s.Pool, s.Res.TenantID)
		_, err := tx.Exec(ctx, sql)
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("column grant %s: %v", sql, err)
		}
		_ = tx.Rollback(ctx)
	}
	tx = dbtest.AppTx(t, s.Pool, s.Res.TenantID)
	var dealGrants bool
	if err := tx.QueryRow(ctx, `SELECT has_column_privilege('salon_app','groupbuy_deals','name','UPDATE') AND has_column_privilege('salon_app','groupbuy_deals','purchase_price','UPDATE') AND has_column_privilege('salon_app','groupbuy_deals','item_id','UPDATE') AND has_column_privilege('salon_app','groupbuy_deals','active','UPDATE') AND NOT has_column_privilege('salon_app','groupbuy_deals','platform','UPDATE') AND NOT has_column_privilege('salon_app','groupbuy_deals','created_by','UPDATE') AND NOT has_table_privilege('salon_app','groupbuy_deals','UPDATE')`).Scan(&dealGrants); err != nil || !dealGrants {
		t.Fatal("groupbuy deal column grants", dealGrants, err)
	}
	_ = tx.Rollback(ctx)
}

func TestFakeScope(t *testing.T) {
	f := fakeprovider.New("douyin", instant)
	ctx := context.Background()
	in := request("same local key")
	in.TenantID = uuid.New()
	in.StoreID = uuid.New()
	if _, err := f.Redeem(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Key = "revoke:" + in.Key
	if _, err := f.Revoke(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Key = "same local key"
	in.TenantID = uuid.New()
	in.StoreID = uuid.New()
	result, err := f.Redeem(ctx, in)
	if err != nil || result.Status != "redeemed" {
		t.Fatalf("cross tenant key reuse: %+v %v", result, err)
	}
	result, err = f.Query(ctx, in)
	if err != nil || result.Status != "redeemed" {
		t.Fatalf("cross tenant state: %+v %v", result, err)
	}
}

func TestLateRevoke(t *testing.T) {
	s, f, c := setup(t)
	ctx := context.Background()
	in := request("late revoke")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	f.Replies["revoke"] = []fakeprovider.Reply{{Err: groupbuy.ErrUnknown}}
	f.Before = func(action string, _ groupbuy.CouponRequest) {
		if action != "revoke" {
			return
		}
		if _, err := s.Pool.Exec(ctx, "UPDATE coupon_redemptions SET status='revoked', revoked_at=$1 WHERE idempotency_key=$2", instant, in.Key); err != nil {
			t.Fatal(err)
		}
	}
	success(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: in.Key}), "revoked")
}

func TestAuditFailureRecovery(t *testing.T) {
	s, f, c := setup(t)
	ctx := context.Background()
	in := request("audit failure")
	if _, err := s.Pool.Exec(ctx, "REVOKE INSERT ON audit_log FROM salon_app"); err != nil {
		t.Fatal(err)
	}
	r := s.HTTP(t, &c, "redeem_coupon", in)
	// rejection-detail: 内部数据库写入失败统一映射 500，此处检查状态回滚及恢复路径。
	if r.Status != 500 {
		t.Fatalf("audit failure: %+v", r)
	}
	if _, err := s.Pool.Exec(ctx, "GRANT INSERT ON audit_log TO salon_app"); err != nil {
		t.Fatal(err)
	}
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: in.Key}), "pending")
	success(t, s.HTTP(t, &c, "redeem_coupon", in), "redeemed")
	if f.Count("redeem") != 1 {
		t.Fatal("audit failure caused another external redeem")
	}
	auditCount(t, s, "redeem_coupon", 1)
}

func TestRecordKeyFilters(t *testing.T) {
	s, f, c := setup(t)
	ctx := context.Background()
	success(t, s.HTTP(t, &c, "redeem_coupon", request("first")), "redeemed")
	for _, ch := range s.Channels() {
		rejection(t, ch.Call(t, c, "get_redemption", groupbuy.LookupInput{Key: "absent"}), 404, "not found")
	}
	rejection(t, s.HTTP(t, &c, "revoke_coupon", groupbuy.LookupInput{Key: "absent"}), 404, "not found")
	f.Replies["redeem"] = []fakeprovider.Reply{{Err: errors.New("declined")}}
	rejection(t, s.HTTP(t, &c, "redeem_coupon", request("second")), 409, "团购平台操作失败")
	success(t, s.AI(t, c, "get_redemption", groupbuy.LookupInput{Key: "first"}), "redeemed")
	err := db.InTenant(ctx, s.Pool, s.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := q.UpdateRedemption(ctx, dbgen.UpdateRedemptionParams{StoreID: uuid.New(), IdempotencyKey: "first", Status: "failed"})
		return err
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update crossed store boundary: %v", err)
	}
}

func TestProviderScopes(t *testing.T) {
	s, f, c := setup(t)
	f.Before = func(_ string, r groupbuy.CouponRequest) {
		if r.TenantID != c.TenantID || r.StoreID != c.StoreID {
			t.Error("provider scope missing")
		}
	}
	success(t, s.HTTP(t, &c, "verify_coupon", request("scope")), "unused")
	success(t, s.HTTP(t, &c, "redeem_coupon", request("scope")), "redeemed")
}
