package interaction

import (
	"testing"
)

func TestOutcomeCursorRejectsMalformedInput(t *testing.T) {
	for _, encoded := range []string{
		"not-base64",
		"e30",
		"eyJvY2N1cnJlZEF0IjoiMjAyNi0wOC0xMFQwOTozMDowMFoiLCJpZCI6Im5vdC1hLXV1aWQifQ",
	} {
		if _, err := decodeOutcomeCursor(encoded); err == nil {
			t.Fatalf("decode malformed AI outcome cursor %q succeeded", encoded)
		}
	}
}
