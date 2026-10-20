package staff_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/staff"
)

const ownerPassword = "owner-password"

type shop struct {
	t     *testing.T
	pool  *pgxpool.Pool
	app   *optest.App
	res   staff.BootstrapResult
	owner string // 老板的登录凭证
}

func open(t *testing.T) *shop {
	t.Helper()
	pool := dbtest.NewPool(t)
	res, err := staff.Bootstrap(context.Background(), pool, staff.BootstrapInput{
		TenantName: "本店", StoreName: "本店", OwnerName: "王老板", OwnerNumber: "001",
		OwnerAccount: "boss", OwnerPassword: ownerPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &shop{t: t, pool: pool, app: optest.New(t, pool), res: res}
	s.owner = s.login("boss", ownerPassword)
	return s
}

func (s *shop) login(account, password string) string {
	s.t.Helper()
	code, token := s.app.Login(s.t, account, password)
	if code != http.StatusOK {
		s.t.Fatalf("login %s: %d", account, code)
	}
	return token
}

// call 用登录凭证通过 HTTP 调用；want 不为 0 时检查状态码。
func (s *shop) call(token, op string, args any, want int) optest.Result {
	s.t.Helper()
	r := s.app.HTTPWithToken(s.t, token, op, args)
	if want != 0 && r.Status != want {
		s.t.Fatalf("%s: got %d %v, want %d", op, r.Status, r.Body, want)
	}
	return r
}

// ai 以登录凭证对应的员工本人身份调用 AI 工具。
func (s *shop) ai(token, op string, args any) optest.Result {
	s.t.Helper()
	caller, ok := s.app.CallerFor(s.t, token)
	if !ok {
		return optest.Result{Status: http.StatusUnauthorized}
	}
	return s.app.AI(s.t, caller, op, args)
}

// hire 由老板建一名有登录账号的员工，返回员工 ID 和登录凭证。
func (s *shop) hire(name, number, account string, roles ...uuid.UUID) (uuid.UUID, string) {
	s.t.Helper()
	if roles == nil {
		roles = []uuid.UUID{}
	}
	r := s.call(s.owner, "create_staff", map[string]any{"name": name, "number": number, "role_ids": roles}, http.StatusOK)
	id := uuid.MustParse(r.Body["id"].(string))
	s.call(s.owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": account, "password": "password-" + account}, http.StatusOK)
	return id, s.login(account, "password-"+account)
}

func (s *shop) role(name string) uuid.UUID { return s.res.Roles[name] }

func permissions(r optest.Result) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, p := range r.Body["permissions"].([]any) {
		m := p.(map[string]any)
		out[m["permission"].(string)] = m
	}
	return out
}

func TestOwnerLogsInWithAllPermissions(t *testing.T) {
	s := open(t)
	me := s.call(s.owner, "get_me", map[string]any{}, http.StatusOK)
	if me.Body["is_owner"] != true {
		t.Fatalf("is_owner = %v, want true", me.Body["is_owner"])
	}
	if got := len(permissions(me)); got != len(perm.Catalogue) {
		t.Fatalf("owner has %d permissions, want all %d", got, len(perm.Catalogue))
	}
}

func TestLoginRejectsWrongPasswordAndUnknownAccount(t *testing.T) {
	s := open(t)
	for _, c := range []struct{ account, password string }{{"boss", "wrong-password"}, {"nobody", ownerPassword}} {
		if code, _ := s.app.Login(t, c.account, c.password); code != http.StatusUnauthorized {
			t.Errorf("login %s/%s = %d, want 401", c.account, c.password, code)
		}
	}
}

func TestPasswordsAreStoredOnlyAsHashes(t *testing.T) {
	s := open(t)
	s.hire("小李", "002", "xiaoli")
	rows, err := s.pool.Query(context.Background(), "SELECT login_account, password_hash FROM staff WHERE login_account IS NOT NULL")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var account, hash string
		if err := rows.Scan(&account, &hash); err != nil {
			t.Fatal(err)
		}
		n++
		if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, "password") {
			t.Errorf("%s: stored %q, want an argon2id hash only", account, hash)
		}
	}
	if n != 2 {
		t.Fatalf("checked %d accounts, want 2", n)
	}
}

func TestDeactivatedStaffCannotLoginButKeepsName(t *testing.T) {
	s := open(t)
	id, token := s.hire("小李", "002", "xiaoli", s.role("发型师"))
	s.call(token, "get_me", map[string]any{}, http.StatusOK)

	s.call(s.owner, "deactivate_staff", map[string]any{"staff_id": id}, http.StatusOK)

	// 已签发的凭证立即失效，也不能再登录。
	s.call(token, "get_me", map[string]any{}, http.StatusUnauthorized)
	if code, _ := s.app.Login(t, "xiaoli", "password-xiaoli"); code != http.StatusUnauthorized {
		t.Fatalf("deactivated login = %d, want 401", code)
	}

	// 员工记录保留，历史单据仍能显示姓名。
	list := s.call(s.owner, "list_staff", map[string]any{}, http.StatusOK)
	var found map[string]any
	for _, st := range list.Body["staff"].([]any) {
		if m := st.(map[string]any); m["id"] == id.String() {
			found = m
		}
	}
	if found == nil || found["name"] != "小李" || found["active"] != false || found["deactivated_at"] == nil {
		t.Fatalf("deactivated staff in list = %v, want name kept and inactive", found)
	}

	// 恢复后可以再登录。
	s.call(s.owner, "reactivate_staff", map[string]any{"staff_id": id}, http.StatusOK)
	s.login("xiaoli", "password-xiaoli")
}

func TestStaffRowsCannotBeDeleted(t *testing.T) {
	s := open(t)
	id, _ := s.hire("小李", "002", "xiaoli")
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", s.res.TenantID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM staff WHERE id = $1", id)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("delete staff: err = %v, want permission denied", err)
	}
}

func TestDefaultRolesMatchADR(t *testing.T) {
	s := open(t)
	roles := s.call(s.owner, "list_roles", map[string]any{}, http.StatusOK).Body["roles"].([]any)
	if len(roles) != 1+len(perm.DefaultRoles) {
		t.Fatalf("got %d roles, want owner + %d defaults", len(roles), len(perm.DefaultRoles))
	}
	var order []string
	byName := map[string]map[string]any{}
	for _, r := range roles {
		order = append(order, r.(map[string]any)["name"].(string))
		m := r.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if got := strings.Join(order, ","); got != "老板,店长,发型师,助理,前台" {
		t.Errorf("role order = %s, want 老板,店长,发型师,助理,前台", got)
	}
	if byName["老板"]["is_owner"] != true {
		t.Fatalf("老板 role = %v, want is_owner", byName["老板"])
	}
	for _, d := range perm.DefaultRoles {
		got := permissions(optest.Result{Body: byName[d.Name]})
		if len(got) != len(d.Grants) {
			t.Errorf("%s: %d permissions, want %d", d.Name, len(got), len(d.Grants))
		}
	}
	stylist := permissions(optest.Result{Body: byName["发型师"]})
	if e := stylist["order.edit"]; e["scope"] != "self" || e["window_days"] != 0.0 {
		t.Errorf("发型师 order.edit = %v, want self, same day", e)
	}
	if _, ok := stylist["order.void"]; ok {
		t.Errorf("发型师 should not have order.void")
	}
}

func TestInsufficientPermissionRejectedOnBothChannels(t *testing.T) {
	s := open(t)
	_, stylist := s.hire("小李", "002", "xiaoli", s.role("发型师"))
	for _, op := range []string{"list_staff", "create_role"} {
		args := map[string]any{}
		if op == "create_role" {
			args = map[string]any{"name": "x", "permissions": []any{}}
		}
		if r := s.call(stylist, op, args, 0); r.Status != http.StatusForbidden {
			t.Errorf("http %s = %d, want 403", op, r.Status)
		}
	}
	if r := s.ai(stylist, "list_staff", map[string]any{}); r.Status != http.StatusForbidden {
		t.Errorf("ai list_staff = %d, want 403", r.Status)
	}
	// 发型师看得到自己的权限，用于决定显示哪些页面。
	me := permissions(s.call(stylist, "get_me", map[string]any{}, http.StatusOK))
	if _, ok := me["staff.manage"]; ok {
		t.Errorf("发型师 get_me lists staff.manage")
	}
	if _, ok := me["order.create"]; !ok {
		t.Errorf("发型师 get_me missing order.create")
	}
}

func TestMultipleRolesUnionWithWidestLimits(t *testing.T) {
	s := open(t)
	mk := func(name string, scope string, days int) uuid.UUID {
		r := s.call(s.owner, "create_role", map[string]any{"name": name, "permissions": []any{
			map[string]any{"permission": "order.edit", "scope": scope, "window_days": days},
		}}, http.StatusOK)
		return uuid.MustParse(r.Body["id"].(string))
	}
	a := mk("A", "self", 3)
	b := mk("B", "store", 1)
	_, token := s.hire("小李", "002", "xiaoli", a, b, s.role("前台"))

	me := permissions(s.call(token, "get_me", map[string]any{}, http.StatusOK))
	if e := me["order.edit"]; e["scope"] != "store" || e["window_days"] != 3.0 {
		t.Fatalf("order.edit = %v, want store scope and 3 days", e)
	}
	if _, ok := me["payment.collect"]; !ok {
		t.Fatalf("missing payment.collect from 前台 role: %v", me)
	}
}

func TestLastActiveOwnerCannotBeRemoved(t *testing.T) {
	s := open(t)
	owner := s.res.OwnerID
	s.call(s.owner, "set_staff_roles", map[string]any{"staff_id": owner, "role_ids": []any{}}, http.StatusConflict)
	s.call(s.owner, "deactivate_staff", map[string]any{"staff_id": owner}, http.StatusConflict)

	// 有两名老板时可以互相删除，但删到最后一名为止。
	second, _ := s.hire("李老板", "002", "boss2", s.role("老板"))
	s.call(s.owner, "set_staff_roles", map[string]any{"staff_id": second, "role_ids": []any{}}, http.StatusOK)
	s.call(s.owner, "set_staff_roles", map[string]any{"staff_id": owner, "role_ids": []any{}}, http.StatusConflict)
}

// 两名老板同时互相删除时，必须有一个被拒绝。测试自己扮演先到的那一方：
// 锁住老板角色、删掉老板 A，在提交前让另一个请求去删老板 B。另一个请求必须等锁，
// 并在 A 的删除提交后看到只剩 B，从而被拒绝。没有锁的话，两边都会成功，门店没有老板。
func TestConcurrentOwnerRemovalKeepsOneOwner(t *testing.T) {
	s := open(t)
	second, _ := s.hire("李老板", "002", "boss2", s.role("老板"))
	ctx := context.Background()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{"SET LOCAL ROLE salon_app", "SELECT set_config('app.tenant_id', '" + s.res.TenantID.String() + "', true)"} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	q := dbgen.New(tx)
	if _, err := q.LockOwnerRole(ctx, s.res.StoreID); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteStaffRoles(ctx, s.res.OwnerID); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	go func() {
		done <- s.app.HTTPWithToken(t, s.owner, "set_staff_roles",
			map[string]any{"staff_id": second, "role_ids": []any{}}).Status
	}()
	select {
	case status := <-done:
		t.Fatalf("removal finished with %d while the owner role was locked; want it to wait", status)
	case <-time.After(300 * time.Millisecond):
	}
	// 老板 A 的凭证此时还有效（删除尚未提交）。提交后，等待中的请求继续执行。
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if status := <-done; status == http.StatusOK {
		t.Fatalf("second removal succeeded; want it rejected so one owner remains")
	}

	var owners int64
	err = db.InTenant(ctx, s.pool, s.res.TenantID, func(q *dbgen.Queries) error {
		var err error
		owners, err = q.CountActiveOwners(ctx, s.res.StoreID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("%d active owners, want 1", owners)
	}
}

func TestOnlyOwnerGrantsOrRemovesOwnerRole(t *testing.T) {
	s := open(t)
	_, manager := s.hire("张店长", "002", "manager", s.role("店长"))
	other, _ := s.hire("小李", "003", "xiaoli")
	s.call(manager, "set_staff_roles", map[string]any{"staff_id": other, "role_ids": []any{s.role("老板")}}, http.StatusForbidden)
	s.call(manager, "set_staff_roles", map[string]any{"staff_id": s.res.OwnerID, "role_ids": []any{}}, http.StatusForbidden)
	// 店长可以分配不超过自己的角色。
	s.call(manager, "set_staff_roles", map[string]any{"staff_id": other, "role_ids": []any{s.role("发型师")}}, http.StatusOK)
}

func TestCannotGrantBeyondOwnPermissions(t *testing.T) {
	s := open(t)
	// 店长默认没有角色权限管理；建一个"店长 + 角色权限管理"的角色给他。
	roles := s.call(s.owner, "list_roles", map[string]any{}, http.StatusOK).Body["roles"].([]any)
	var managerPerms []any
	for _, r := range roles {
		if m := r.(map[string]any); m["name"] == "店长" {
			managerPerms = m["permissions"].([]any)
		}
	}
	managerPerms = append(managerPerms, map[string]any{"permission": "role.manage"})
	r := s.call(s.owner, "create_role", map[string]any{"name": "店长加角色管理", "permissions": managerPerms}, http.StatusOK)
	_, manager := s.hire("张店长", "002", "manager", uuid.MustParse(r.Body["id"].(string)))

	editWithin := map[string]any{"permission": "order.edit", "scope": "store", "window_days": 7}
	editBeyond := map[string]any{"permission": "order.edit", "scope": "store", "window_days": 30}
	settings := map[string]any{"permission": "settings.manage"}

	s.call(manager, "create_role", map[string]any{"name": "越权1", "permissions": []any{settings}}, http.StatusForbidden)
	s.call(manager, "create_role", map[string]any{"name": "越权2", "permissions": []any{editBeyond}}, http.StatusForbidden)
	s.call(manager, "create_role", map[string]any{"name": "可以", "permissions": []any{editWithin}}, http.StatusOK)

	// 不能把超过自己的角色分配给别人，也不能管理权限比自己大的员工。
	settingsRole := s.call(s.owner, "create_role", map[string]any{"name": "设置", "permissions": []any{settings}}, http.StatusOK)
	other, _ := s.hire("小李", "003", "xiaoli")
	s.call(manager, "set_staff_roles", map[string]any{"staff_id": other, "role_ids": []any{settingsRole.Body["id"]}}, http.StatusForbidden)
	s.call(manager, "set_staff_login", map[string]any{"staff_id": s.res.OwnerID, "login_account": "boss", "password": "taken-over"}, http.StatusForbidden)
	s.call(manager, "deactivate_staff", map[string]any{"staff_id": s.res.OwnerID}, http.StatusForbidden)
	s.call(manager, "update_role", map[string]any{"role_id": settingsRole.Body["id"], "name": "设置", "permissions": []any{}}, http.StatusForbidden)

	// 老板角色谁都不能改。
	s.call(s.owner, "update_role", map[string]any{"role_id": s.role("老板"), "name": "老板", "permissions": []any{}}, http.StatusConflict)
}

func TestRevokedPermissionTakesEffectOnNextRequest(t *testing.T) {
	s := open(t)
	id, manager := s.hire("张店长", "002", "manager", s.role("店长"))
	s.call(manager, "list_staff", map[string]any{}, http.StatusOK)
	if r := s.ai(manager, "list_staff", map[string]any{}); r.Status != http.StatusOK {
		t.Fatalf("ai list_staff before revoke = %d", r.Status)
	}

	s.call(s.owner, "set_staff_roles", map[string]any{"staff_id": id, "role_ids": []any{s.role("发型师")}}, http.StatusOK)

	s.call(manager, "list_staff", map[string]any{}, http.StatusForbidden)
	if r := s.ai(manager, "list_staff", map[string]any{}); r.Status != http.StatusForbidden {
		t.Fatalf("ai list_staff after revoke = %d, want 403", r.Status)
	}
}

func TestChangeMyPassword(t *testing.T) {
	s := open(t)
	_, token := s.hire("小李", "002", "xiaoli")
	s.call(token, "change_my_password", map[string]any{"old_password": "wrong", "new_password": "new-password"}, http.StatusConflict)
	s.call(token, "change_my_password", map[string]any{"old_password": "password-xiaoli", "new_password": "new-password"}, http.StatusOK)
	s.login("xiaoli", "new-password")
}

func TestDuplicateNumberAndAccountRejected(t *testing.T) {
	s := open(t)
	s.call(s.owner, "create_staff", map[string]any{"name": "重号", "number": "001", "role_ids": []any{}}, http.StatusConflict)
	id, _ := s.hire("小李", "002", "xiaoli")
	s.call(s.owner, "set_staff_login", map[string]any{"staff_id": id, "login_account": "boss", "password": "password-x"}, http.StatusConflict)
}

func TestLevelsAndPositions(t *testing.T) {
	s := open(t)
	lvl := s.call(s.owner, "create_level", map[string]any{"name": "首席"}, http.StatusOK)
	pos := s.call(s.owner, "create_position", map[string]any{"name": "大工"}, http.StatusOK)
	r := s.call(s.owner, "create_staff", map[string]any{
		"name": "小李", "number": "002", "level_id": lvl.Body["id"], "position_id": pos.Body["id"], "role_ids": []any{},
	}, http.StatusOK)
	if r.Body["level"].(map[string]any)["name"] != "首席" || r.Body["position"].(map[string]any)["name"] != "大工" {
		t.Fatalf("staff = %v, want level 首席 and position 大工", r.Body)
	}
	s.call(s.owner, "create_staff", map[string]any{
		"name": "小王", "number": "003", "level_id": uuid.New(), "role_ids": []any{},
	}, http.StatusConflict)
}

func TestCannotChangeRolesOfStaffWithMorePermissions(t *testing.T) {
	s := open(t)
	roles := s.call(s.owner, "list_roles", map[string]any{}, http.StatusOK).Body["roles"].([]any)
	var managerPerms []any
	for _, r := range roles {
		if m := r.(map[string]any); m["name"] == "店长" {
			managerPerms = m["permissions"].([]any)
		}
	}
	senior := s.call(s.owner, "create_role", map[string]any{"name": "高级店长",
		"permissions": append(managerPerms, map[string]any{"permission": "role.manage"})}, http.StatusOK)
	superior, _ := s.hire("赵总", "002", "zhao", uuid.MustParse(senior.Body["id"].(string)))
	_, manager := s.hire("张店长", "003", "manager", s.role("店长"))

	// 店长能授予"前台"，但对方权限比自己大，不能改对方的角色。
	s.call(manager, "set_staff_roles", map[string]any{"staff_id": superior, "role_ids": []any{s.role("前台")}}, http.StatusForbidden)
	s.call(manager, "set_staff_roles", map[string]any{"staff_id": superior,
		"role_ids": []any{senior.Body["id"], s.role("前台")}}, http.StatusForbidden)
}

func TestInactiveLevelCannotBeAssignedButCanBeKept(t *testing.T) {
	s := open(t)
	lvl := s.call(s.owner, "create_level", map[string]any{"name": "首席"}, http.StatusOK)
	id := s.call(s.owner, "create_staff", map[string]any{
		"name": "小李", "number": "002", "level_id": lvl.Body["id"], "role_ids": []any{},
	}, http.StatusOK).Body["id"]
	s.call(s.owner, "update_level", map[string]any{"id": lvl.Body["id"], "name": "首席", "active": false}, http.StatusOK)

	// 已有的停用级别可以保留，改别的字段不受影响。
	s.call(s.owner, "update_staff", map[string]any{"staff_id": id, "name": "小李改名", "number": "002", "level_id": lvl.Body["id"]}, http.StatusOK)
	// 不能再分配给其他员工。
	s.call(s.owner, "create_staff", map[string]any{
		"name": "小王", "number": "003", "level_id": lvl.Body["id"], "role_ids": []any{},
	}, http.StatusConflict)
}

func TestRoleListsRequirePermission(t *testing.T) {
	s := open(t)
	_, stylist := s.hire("小李", "002", "xiaoli", s.role("发型师"))
	_, manager := s.hire("张店长", "003", "manager", s.role("店长"))
	cases := []struct {
		token, op string
		want      int
	}{
		{stylist, "list_roles", http.StatusForbidden},
		{stylist, "list_role_options", http.StatusForbidden},
		{manager, "list_roles", http.StatusForbidden},
		{manager, "list_role_options", http.StatusOK},
		{s.owner, "list_roles", http.StatusOK},
		{s.owner, "list_role_options", http.StatusOK},
	}
	for _, c := range cases {
		if r := s.call(c.token, c.op, map[string]any{}, 0); r.Status != c.want {
			t.Errorf("%s by token %.8s… = %d, want %d", c.op, c.token, r.Status, c.want)
		}
		if r := s.ai(c.token, c.op, map[string]any{}); r.Status != c.want {
			t.Errorf("ai %s = %d, want %d", c.op, r.Status, c.want)
		}
	}
	opts := s.call(manager, "list_role_options", map[string]any{}, http.StatusOK).Body["roles"].([]any)
	if _, has := opts[0].(map[string]any)["permissions"]; has {
		t.Fatalf("list_role_options exposes permissions: %v", opts[0])
	}
}

// 员工和角色的写操作在 AI 路径上走不到业务规则：草稿类在草稿功能（P0-29）实现前一律拒绝，
// 不开放的操作对 AI 不存在。即使发起人是老板也一样，数据不变。
func TestAICannotExecuteStaffAndRoleWrites(t *testing.T) {
	s := open(t)
	id, _ := s.hire("小李", "002", "xiaoli")
	cases := []struct {
		op   string
		args map[string]any
		want int
	}{
		{"create_staff", map[string]any{"name": "AI", "number": "099", "role_ids": []any{}}, http.StatusConflict},
		{"update_staff", map[string]any{"staff_id": id, "name": "AI 改名", "number": "002"}, http.StatusConflict},
		{"deactivate_staff", map[string]any{"staff_id": id}, http.StatusConflict},
		{"set_staff_roles", map[string]any{"staff_id": id, "role_ids": []any{s.role("店长")}}, http.StatusConflict},
		{"create_level", map[string]any{"name": "AI 级别"}, http.StatusConflict},
		{"set_staff_login", map[string]any{"staff_id": id, "login_account": "ai-account", "password": "ai-password"}, http.StatusNotFound},
		{"create_role", map[string]any{"name": "AI 角色", "permissions": []any{}}, http.StatusNotFound},
		{"update_role", map[string]any{"role_id": s.role("前台"), "name": "AI", "permissions": []any{}}, http.StatusNotFound},
		{"delete_role", map[string]any{"role_id": s.role("前台")}, http.StatusNotFound},
		{"change_my_password", map[string]any{"old_password": ownerPassword, "new_password": "ai-password"}, http.StatusNotFound},
	}
	before := s.call(s.owner, "list_staff", map[string]any{}, http.StatusOK).Body
	rolesBefore := s.call(s.owner, "list_roles", map[string]any{}, http.StatusOK).Body
	for _, c := range cases {
		if r := s.ai(s.owner, c.op, c.args); r.Status != c.want {
			t.Errorf("ai %s = %d (%v), want %d", c.op, r.Status, r.Err, c.want)
		}
	}
	after := s.call(s.owner, "list_staff", map[string]any{}, http.StatusOK).Body
	rolesAfter := s.call(s.owner, "list_roles", map[string]any{}, http.StatusOK).Body
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(rolesBefore, rolesAfter) {
		t.Fatalf("AI calls changed data")
	}
	s.login("boss", ownerPassword)
}
