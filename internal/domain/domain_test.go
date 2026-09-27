package domain

import "testing"

func TestDecimal(t *testing.T) {
	for _, tc := range []struct{ in, out string }{{"1.00", "1"}, {"000.0100", "0.01"}, {"0", "0"}} {
		v, err := Decimal(tc.in, true)
		if err != nil || v != tc.out {
			t.Fatalf("%q => %q %v", tc.in, v, err)
		}
	}
	for _, s := range []string{"-1", "1e2", "1.", ".1", "1234567890123456789", "0.0000000000000000001"} {
		if _, err := Decimal(s, true); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	if CompareDecimal("1.10", "1.2") >= 0 || CompareDecimal("10", "2") <= 0 {
		t.Fatal("decimal ordering")
	}
}
