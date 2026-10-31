package dbtest

import (
	"errors"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRunPostgres(t *testing.T) {
	reaperErr := errors.New("create container: reaper: new reaper: No such container")
	otherErr := errors.New("pull postgres: unavailable")
	for _, tc := range []struct {
		name  string
		first error
		next  error
		calls int
		want  error
	}{
		{name: "success", calls: 1},
		{name: "reaper recovers", first: reaperErr, calls: 2},
		{name: "reaper fails twice", first: reaperErr, next: reaperErr, calls: 2, want: reaperErr},
		{name: "retry returns other error", first: reaperErr, next: otherErr, calls: 2, want: otherErr},
		{name: "other error is not retried", first: otherErr, calls: 1, want: otherErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container := &postgres.PostgresContainer{}
			calls := 0
			got, err := runPostgres(func() (*postgres.PostgresContainer, error) {
				calls++
				failure := tc.first
				if calls > 1 {
					failure = tc.next
				}
				if failure != nil {
					return nil, failure
				}
				return container, nil
			})
			if calls != tc.calls || !errors.Is(err, tc.want) {
				t.Fatalf("calls=%d, err=%v; want calls=%d, err=%v", calls, err, tc.calls, tc.want)
			}
			if tc.want == nil && got != container {
				t.Fatal("successful run did not return its container")
			}
		})
	}
}
