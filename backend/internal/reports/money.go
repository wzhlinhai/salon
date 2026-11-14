package reports

import (
	"sort"
	"strconv"
	"strings"

	"github.com/wzhlinhai/salon/backend/internal/money"
)

func change(value string) (money.Change, error) {
	var out money.Change
	if err := out.Scan(value); err != nil {
		return out, err
	}
	return out, nil
}

func addAmount(target map[string]money.SignedTotal, key string, value string) error {
	delta, err := change(value)
	if err != nil {
		return err
	}
	target[key] = target[key].Add(delta)
	return nil
}

func addSigned(target *money.SignedTotal, value string) error {
	delta, err := change(value)
	if err != nil {
		return err
	}
	*target = target.Add(delta)
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// share is a four-decimal guest count stored as ten-thousandths.
func parseShare(value string) (int64, error) {
	whole, fraction, _ := strings.Cut(value, ".")
	if len(fraction) > 4 {
		fraction = fraction[:4]
	}
	fraction += strings.Repeat("0", 4-len(fraction))
	n, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func formatShare(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / 10000
	fraction := value % 10000
	return sign + strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(fraction+10000, 10)[1:]
}
