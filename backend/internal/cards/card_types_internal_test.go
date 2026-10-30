package cards

import "testing"

func TestValidateTypeDaysMaximum(t *testing.T) {
	for _, days := range []int32{36500, 36501} {
		in := &CreateCardTypeInput{Name: "百年卡", Kind: StoredValue, ValidityKind: "days_after_open", ValidityDays: &days}
		_, err := validateType(in)
		if days == 36500 && err != nil {
			t.Fatal(err)
		}
		if days == 36501 && (err == nil || err.Error() != "开卡后天数不能超过 36500 天") {
			t.Fatal(err)
		}
	}
}
