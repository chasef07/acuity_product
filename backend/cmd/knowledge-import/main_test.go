package main

import (
	"strings"
	"testing"
)

func TestImportFileCannotForgeActorOrIgnoreExtraInput(t *testing.T) {
	for _, body := range []string{`{"actorSubject":"someone-else"}`, `{} {}`, `{"patientId":"synthetic-patient"}`} {
		if _, err := readCommand(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted unsafe import input %s", body)
		}
	}
	if _, err := readCommand(strings.NewReader(`{"officeKey":"synthetic-office","sections":[{"id":"hours","title":"Hours","text":"Monday closes at 5pm."}]}`)); err != nil {
		t.Fatal(err)
	}
}
