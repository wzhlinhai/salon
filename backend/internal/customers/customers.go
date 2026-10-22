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
	items, err := views(ctx, env, []dbgen.Customer{row})
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

// views 批量构造顾客视图，保持输入顺序；空列表不查询。
func views(ctx context.Context, env ops.Env, rows []dbgen.Customer) ([]*Customer, error) {
	out := make([]*Customer, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	sourceIDs, staffIDs, customerIDs := []uuid.UUID{}, []uuid.UUID{}, []uuid.UUID{}
	for _, row := range rows {
		customerIDs = append(customerIDs, row.ID)
		if row.SourceID != nil {
			sourceIDs = append(sourceIDs, *row.SourceID)
		}
		if row.ResponsibleStaffID != nil {
			staffIDs = append(staffIDs, *row.ResponsibleStaffID)
		}
	}
	sources, staff := map[uuid.UUID]*CustomerRef{}, map[uuid.UUID]*CustomerRef{}
	if len(sourceIDs) > 0 {
		refs, err := env.Q.ListCustomerViewSources(ctx, sourceIDs)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			sources[ref.ID] = &CustomerRef{ID: ref.ID, Name: ref.Name}
		}
	}
	if len(staffIDs) > 0 {
		refs, err := env.Q.ListCustomerViewStaff(ctx, staffIDs)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			staff[ref.ID] = &CustomerRef{ID: ref.ID, Name: ref.Name}
		}
	}
	labels, err := env.Q.ListCustomerViewSafetyLabels(ctx, customerIDs)
	if err != nil {
		return nil, err
	}
	byCustomer := map[uuid.UUID][]SafetyLabel{}
	for _, l := range labels {
		byCustomer[l.CustomerID] = append(byCustomer[l.CustomerID], SafetyLabel{ID: l.ID, Kind: l.Kind, Source: l.Source, Note: l.Note, ObservedOn: l.ObservedOn.Time.Format(time.DateOnly), RecordedBy: l.RecordedBy, CreatedAt: l.CreatedAt.Time})
	}
	for _, row := range rows {
		c := &Customer{ID: row.ID, Name: row.Name, Phone: row.Phone, Gender: row.Gender, Note: row.Note, Unidentified: row.Phone == nil, MergedInto: row.MergedInto, CreatedAt: row.CreatedAt.Time, ActiveSafetyLabels: []SafetyLabel{}}
		if row.SourceID != nil {
			c.Source = sources[*row.SourceID]
		}
		if row.ResponsibleStaffID != nil {
			c.Responsible = staff[*row.ResponsibleStaffID]
		}
		if labels := byCustomer[row.ID]; labels != nil {
			c.ActiveSafetyLabels = labels
		}
		out = append(out, c)
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

// save 保存持锁顾客的资料。
func save(ctx context.Context, env ops.Env, row dbgen.Customer) error {
	return env.Q.SaveCustomer(ctx, dbgen.SaveCustomerParams{ID: row.ID, Name: row.Name, Phone: row.Phone, Gender: row.Gender, SourceID: row.SourceID, ResponsibleStaffID: row.ResponsibleStaffID, Note: row.Note, MergedInto: row.MergedInto})
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

// CanView 判断调用者是否有权查看顾客，供关联顾客的业务操作共用。
func CanView(caller ops.Caller, row dbgen.Customer) bool { return canView(caller, row) }
