package middleware

import (
	"strconv"
	"testing"
	"time"
)

func TestFreshTimestamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := map[string]bool{
		strconv.FormatInt(now.Unix(), 10):      true,
		strconv.FormatInt(now.Unix()-240, 10):  true,
		strconv.FormatInt(now.Unix()-600, 10):  false,
		strconv.FormatInt(now.Unix()+600, 10):  false,
		strconv.FormatInt(now.UnixMilli(), 10): true,
		"abc":                                  false,
		"":                                     false,
	}
	for ts, want := range cases {
		if got := freshTimestamp(ts, now); got != want {
			t.Errorf("freshTimestamp(%q) = %v, mau %v", ts, got, want)
		}
	}
}
