// Package inforce 按营业日选出带生效日期的修改中执行的那一条：门店设置、提成方案版本、底薪共用这一条规则。
//
// 某一天执行的是生效日期不晚于这一天的最大者；同一生效日期以最后保存的为准（保存序号、保存时刻，再按 ID）。
package inforce

import (
	"bytes"
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Change 是一条从 From 起执行的修改，Seq、SavedAt 和 ID 决定同一生效日期的先后。
type Change[V any] struct {
	From time.Time
	// 保存序号，历史在锁内递增分配时提供（例如提成方案版本号），没有时为 0
	Seq     int64
	SavedAt time.Time
	ID      uuid.UUID
	Value   V
}

// Timeline 是一个键的全部修改，按生效日期、保存序号、保存时刻、ID 升序。
type Timeline[V any] struct {
	changes []Change[V]
}

func compare[V any](a, b Change[V]) int {
	if c := a.From.Compare(b.From); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Seq, b.Seq); c != 0 {
		return c
	}
	if c := a.SavedAt.Compare(b.SavedAt); c != 0 {
		return c
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

// New 用一个键的全部修改构造 Timeline，不依赖输入顺序。
func New[V any](changes []Change[V]) Timeline[V] {
	sorted := slices.Clone(changes)
	slices.SortFunc(sorted, compare)
	return Timeline[V]{changes: sorted}
}

// Group 按 key 把修改分成各自的 Timeline。
func Group[K comparable, V any](changes []Change[V], key func(Change[V]) K) map[K]Timeline[V] {
	byKey := map[K][]Change[V]{}
	for _, c := range changes {
		k := key(c)
		byKey[k] = append(byKey[k], c)
	}
	out := make(map[K]Timeline[V], len(byKey))
	for k, cs := range byKey {
		out[k] = New(cs)
	}
	return out
}

// At 返回营业日 day 执行的修改，同日按 Seq、SavedAt、ID 取最后保存的一条；没有生效日期不晚于 day 的修改时返回 false。
func (t Timeline[V]) At(day time.Time) (Change[V], bool) {
	// 从后往前第一条生效日期不晚于 day 的，就是生效日期最大、同日最后保存的。
	for _, c := range slices.Backward(t.changes) {
		if !c.From.After(day) {
			return c, true
		}
	}
	return Change[V]{}, false
}

// After 返回生效日期晚于 day 的修改，每个生效日期按 Seq、SavedAt、ID 取最后保存的一条，按生效日期升序。
func (t Timeline[V]) After(day time.Time) []Change[V] {
	var out []Change[V]
	for _, c := range t.changes {
		if !c.From.After(day) {
			continue
		}
		if n := len(out); n > 0 && out[n-1].From.Equal(c.From) {
			out[n-1] = c
			continue
		}
		out = append(out, c)
	}
	return out
}
