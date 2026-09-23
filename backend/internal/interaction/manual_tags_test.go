package interaction

import (
	"strings"
	"testing"
)

func TestNormalizeManualTag(t *testing.T) {
	for _, value := range []string{"", " \t ", strings.Repeat("a", 61), "bad\x00tag"} {
		if _, err := normalizeManualTag(value); err == nil {
			t.Errorf("accepted invalid tag %q", value)
		}
	}
	got, err := normalizeManualTag("  Good  \t recovery ")
	if err != nil || got != "Good recovery" {
		t.Fatalf("normalization = %q, %v", got, err)
	}
}
