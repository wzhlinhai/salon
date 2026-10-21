// Package customers 管理租户的顾客档案、安全标签与负责人交接。
package customers

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/audit"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/perm"
	"github.com/wzhlinhai/salon/backend/internal/privacy"
)

// CustomerRef 是来源或负责员工的名称引用。
type CustomerRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// SafetyLabel 是顾客当前有效的安全提醒。
type SafetyLabel struct {
	ID         uuid.UUID `json:"id"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	Note       *string   `json:"note"`
	ObservedOn string    `json:"observed_on"`
	RecordedBy uuid.UUID `json:"recorded_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// Customer 是顾客档案的返回视图。
type Customer struct {
	ID                 uuid.UUID     `json:"id"`
	Name               *string       `json:"name"`
	Phone              *string       `json:"phone"`
	PhoneMasked        bool          `json:"phone_masked"`
	Gender             *string       `json:"gender"`
	Source             *CustomerRef  `json:"source"`
	Responsible        *CustomerRef  `json:"responsible"`
	Note               *string       `json:"note"`
	Unidentified       bool          `json:"unidentified"`
	MergedInto         *uuid.UUID    `json:"merged_into"`
	CreatedAt          time.Time     `json:"created_at"`
	ActiveSafetyLabels []SafetyLabel `json:"active_safety_labels"`
}

// CustomerSource 是可停用的顾客来源。
type CustomerSource struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

// sourceView 返回来源的公开字段。
func sourceView(s dbgen.CustomerSource) CustomerSource {
	return CustomerSource{ID: s.ID, Name: s.Name, Active: s.Active}
}

// view 构造包含来源、负责人和有效安全标签的顾客视图。
func view(ctx context.Context, env ops.Env, row dbgen.Customer) (*Customer, error) {
	out := &Customer{ID: row.ID, Name: row.Name, Phone: row.Phone, Gender: row.Gender, Note: row.Note, Unidentified: row.Phone == nil, MergedInto: row.MergedInto, CreatedAt: row.CreatedAt.Time, ActiveSafetyLabels: []SafetyLabel{}}
	if row.SourceID != nil {
		s, err := env.Q.GetCustomerSource(ctx, *row.SourceID)
		if err != nil {
			return nil, err
		}
		out.Source = &CustomerRef{ID: s.ID, Name: s.Name}
	}
	if row.ResponsibleStaffID != nil {
		s, err := env.Q.GetStaff(ctx, *row.ResponsibleStaffID)
		if err != nil {
			return nil, err
		}
		out.Responsible = &CustomerRef{ID: s.ID, Name: s.Name}
	}
	labels, err := env.Q.ListSafetyLabels(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		out.ActiveSafetyLabels = append(out.ActiveSafetyLabels, SafetyLabel{ID: l.ID, Kind: l.Kind, Source: l.Source, Note: l.Note, ObservedOn: l.ObservedOn.Time.Format(time.DateOnly), RecordedBy: l.RecordedBy, CreatedAt: l.CreatedAt.Time})
	}
	return out, nil
}

// mask 按完整手机号权限处理返回给调用者的顾客视图。
func mask(env ops.Env, c *Customer) *Customer {
	if c.Phone != nil && !env.Caller.Has(perm.CustomerFullPhone) {
		c.Phone = new(privacy.MaskPhone(*c.Phone))
		c.PhoneMasked = true
	}
	return c
}

// rowError 将不存在和已知唯一约束冲突转成业务错误。
func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ops.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		switch pg.ConstraintName {
		case "customer_sources_store_id_name_key":
			return ops.Rule("顾客来源名称已存在")
		case "customers_unique_phone":
			return ops.Rule("这个手机号已属于另一位顾客，可以合并这两位顾客")
		}
	}
	return err
}

// lock 锁住并检查调用者可查看且尚未合并的顾客；只改普通列（手机号仅有部分唯一索引），用 NO KEY UPDATE。
func lock(ctx context.Context, env ops.Env, id uuid.UUID) (dbgen.Customer, error) {
	row, err := env.Q.LockCustomer(ctx, id)
	if err != nil {
		return row, rowError(err)
	}
	if !canView(env.Caller, row) {
		return row, ops.ErrNotFound
	}
	if row.MergedInto != nil {
		return row, ops.Rule("该顾客已合并到另一位顾客")
	}
	return row, nil
}

// save 保存持锁顾客的资料。
func save(ctx context.Context, env ops.Env, row dbgen.Customer) error {
	return env.Q.SaveCustomer(ctx, dbgen.SaveCustomerParams{ID: row.ID, Name: row.Name, Phone: row.Phone, Gender: row.Gender, SourceID: row.SourceID, ResponsibleStaffID: row.ResponsibleStaffID, Note: row.Note, MergedInto: row.MergedInto})
}

// finish 保存顾客并记录完整审计快照，返回按权限打码的视图。
func finish(ctx context.Context, env ops.Env, action string, before *Customer, row dbgen.Customer) (*Customer, error) {
	if err := save(ctx, env, row); err != nil {
		return nil, rowError(err)
	}
	after, err := view(ctx, env, row)
	if err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, env, action, row.ID, before, after); err != nil {
		return nil, err
	}
	return mask(env, after), nil
}

var phonePattern = regexp.MustCompile("^1[0-9]{10}$")

// normalizePhone 去掉空白与短横线并校验大陆手机号。
func normalizePhone(phone *string) (*string, error) {
	if phone == nil {
		return nil, nil
	}
	p := strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, *phone)
	if !phonePattern.MatchString(p) {
		return nil, ops.Rule("请输入有效的中国大陆手机号")
	}
	return &p, nil
}

// checkSource 检查新选来源属于本租户且已启用，允许保留停用来源。
func checkSource(ctx context.Context, env ops.Env, id, current *uuid.UUID) error {
	if id == nil || (current != nil && *current == *id) {
		return nil
	}
	s, err := env.Q.ShareCustomerSource(ctx, *id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !s.Active) {
		return ops.Rule("没有这个顾客来源，或者它已停用")
	}
	return err
}

// activeStaff 共享锁住负责员工并检查其在职状态。
func activeStaff(ctx context.Context, env ops.Env, id uuid.UUID) error {
	s, err := env.Q.ShareCustomerStaff(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !s.Active) {
		return ops.Rule("负责人必须是在职员工")
	}
	return err
}

// viewScope 返回顾客查看权限对应的负责人过滤，没有查看权限则按不存在处理。
func viewScope(caller ops.Caller) (*uuid.UUID, error) {
	ok, self := caller.ScopeFilter(perm.CustomerView)
	if !ok {
		return nil, ops.ErrNotFound
	}
	return self, nil
}

// canView 判断调用者是否有权查看这位顾客，供读取和写入共用。
func canView(caller ops.Caller, row dbgen.Customer) bool {
	self, err := viewScope(caller)
	return err == nil && (self == nil || (row.ResponsibleStaffID != nil && *row.ResponsibleStaffID == *self))
}
