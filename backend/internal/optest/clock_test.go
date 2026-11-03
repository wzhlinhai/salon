package optest

import (
	"testing"
	"time"
)

func TestSetNowAndAdvance(t *testing.T) {
	a := &App{}
	before := time.Now()
	a.Advance(time.Hour)
	after := time.Now()
	got := a.Now()
	if got.Before(before.Add(time.Hour)) || got.After(after.Add(time.Hour)) || !a.Now().Equal(got) {
		t.Fatal("Advance must fix real now plus the duration", got)
	}
	fixed := time.Date(2030, 1, 2, 1, 30, 0, 0, time.UTC)
	a.SetNow(fixed)
	if !a.Now().Equal(fixed) {
		t.Fatal(a.Now())
	}
	a.Advance(5 * time.Minute)
	if !a.Now().Equal(fixed.Add(5 * time.Minute)) {
		t.Fatal(a.Now())
	}
	a.SetNow(time.Time{})
	before = time.Now()
	got = a.Now()
	after = time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatal("zero SetNow must restore real time", got)
	}
	// 恢复后须继续读真实时间，不能冻结为 reset 时的时刻。
	before = time.Now()
	got = a.Now()
	after = time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatal("reset clock must keep advancing", got)
	}
}
