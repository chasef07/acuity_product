package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAuditCommandRequiresAnExplicitDatabase(t *testing.T) {
	var output bytes.Buffer
	err := run(context.Background(), &output, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "AUDIT_DATABASE_URL is required") {
		t.Fatalf("missing audit database error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("missing audit database output = %q", output.String())
	}
}
