package contactcontext

import "testing"

func TestIsCanonicalPhone(t *testing.T) {
	for value, want := range map[string]bool{
		"+13055550100":      true,
		"+447911123456":     true,
		"+1234567":          false,
		"+1234567890123456": false,
		"+03055550100":      false,
		"13055550100":       false,
		" +13055550100":     false,
		"(305) 555-0100":    false,
	} {
		if got := IsCanonicalPhone(value); got != want {
			t.Errorf("IsCanonicalPhone(%q) = %v, want %v", value, got, want)
		}
	}
}
