package garmin

import (
	"encoding/json"
	"testing"
)

func FuzzEpochMilliseconds(f *testing.F) {
	for _, n := range []int64{0, 1792879200000, 1792911600000, 253402300799999, -1} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n int64) {
		b, _ := json.Marshal(n)
		var e EpochMillis
		err := json.Unmarshal(b, &e)
		if n >= 0 && n <= 253402300799999 {
			if err != nil || e.UnixMilli() != n {
				t.Fatal("epoch round trip changed instant")
			}
			encoded, err := json.Marshal(e)
			if err != nil || string(encoded) != string(b) {
				t.Fatalf("fetch export changed epoch milliseconds: %s err=%v", encoded, err)
			}
		} else if err == nil {
			t.Fatal("invalid epoch accepted")
		}
	})
}
