package main

import (
	"strings"
	"testing"
)

func TestImportFileCannotForgeActorOrIgnoreExtraInput(t *testing.T) {
	for _, body := range []string{`{"actorSubject":"someone-else"}`, `{} {}`, `{"patientId":"synthetic-patient"}`, `{"expectedRevisionId":"00000000-0000-4000-8000-000000000001"}`} {
		if _, err := readCommand(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted unsafe import input %s", body)
		}
	}
	if _, err := readCommand(strings.NewReader(`{"officeKey":"synthetic-office","sections":[{"id":"hours","title":"Hours","text":"Monday closes at 5pm."}]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRejectsUnreviewedMetadataAndTrailingDocuments(t *testing.T) {
	for _, body := range []string{
		"practiceId: x\nactorSubject: forged\n",
		"officeKey: a\nofficeKey: b\n",
		"officeKey: a\n---\nofficeKey: b\n",
		"officeKey: a\nentries:\n- id: hours\n  patientId: forbidden\n",
		"officeKey: a\n" + strings.Repeat(" ", 1<<20) + "---\n",
	} {
		if _, err := readSource(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted invalid source: %s", body)
		}
	}
}
