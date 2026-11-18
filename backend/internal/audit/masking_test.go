package audit

import (
	"encoding/json"
	"testing"
)

func TestMaskPhonesPreservesOtherJSONValues(t *testing.T) {
	input := json.RawMessage(`{"phone":"13800131234","customer_phone":"13900131234","children":[{"phone":"01234567890"},{"customer_phone":"01234567890"},{"phone":"1234567890a"},{"phone":13800131234},{"phone":null},{"phone":"138****1234"}],"phone_number":"13800131234","amount":9007199254740993}`)
	got, err := maskPhones(input)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"amount":9007199254740993,"children":[{"phone":"012****7890"},{"customer_phone":"012****7890"},{"phone":"1234567890a"},{"phone":13800131234},{"phone":null},{"phone":"138****1234"}],"customer_phone":"139****1234","phone":"138****1234","phone_number":"13800131234"}`
	if string(got) != want {
		t.Fatalf("masked JSON = %s, want %s", got, want)
	}
}
