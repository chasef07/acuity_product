package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const abitaPracticeID = "31d82880-f0b6-4637-a472-ff5f5b8f74d4"

var requiredAbitaEntries = []string{
	"current-address", "phone", "fax", "paperwork-email", "directions", "other-offices",
	"hours", "holiday-closures", "after-hours",
	"providers", "provider-languages", "scope-of-services",
	"appointment-confirmation", "new-patient-visit", "what-to-bring", "referral-requirements",
	"billing", "payments", "self-pay-pricing",
	"medical-records-requests", "prescription-copies", "social-follow-up",
}

var optionalAbitaEntries = []string{
	"practice-name", "lutz-move-history", "medical-drive-closure",
	"cataract-provider", "routine-vision-and-children", "retina-care",
	"retinal-photo-fee", "staff-task-routing",
	"attorney-records-requests", "records-delivery",
	"contact-lenses", "contact-lens-prescription-validity",
	"frame-brands", "titanium-frames", "meta-eyewear", "lens-options",
	"outside-prescriptions", "eyeglass-prescription-validity", "glasses-turnaround", "glasses-pickup",
	"sunglasses", "optician", "optical-walk-ins", "frame-adjustments", "repairs-and-warranty",
}

const maxAbitaEntryCharacters = 800

func TestAbitaOfficesFollowEntryStandard(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("../../..", "knowledge/offices", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	offices := 0
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		source, err := readSource(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if source.PracticeID != abitaPracticeID {
			continue
		}
		offices++
		t.Run(source.OfficeKey, func(t *testing.T) {
			ids := map[string]bool{}
			titles := map[string]bool{}
			for _, entry := range source.Entries {
				ids[entry.ID] = true
				if !slices.Contains(requiredAbitaEntries, entry.ID) && !slices.Contains(optionalAbitaEntries, entry.ID) {
					t.Errorf("entry %q is not a standard Abita entry", entry.ID)
				}
				if titles[entry.Title] {
					t.Errorf("title %q is used twice", entry.Title)
				}
				titles[entry.Title] = true
				if n := utf8.RuneCountInString(entry.Text); n > maxAbitaEntryCharacters {
					t.Errorf("entry %q has %d characters; split it below %d", entry.ID, n, maxAbitaEntryCharacters)
				}
				for _, line := range strings.Split(entry.Text, "\n") {
					line = strings.TrimSpace(line)
					if strings.Contains(line, "**") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- ") {
						t.Errorf("entry %q uses markdown formatting: %q", entry.ID, line)
					}
				}
			}
			for _, id := range requiredAbitaEntries {
				if !ids[id] {
					t.Errorf("missing required entry %q", id)
				}
			}
		})
	}
	if offices != 5 {
		t.Fatalf("expected 5 Abita offices, found %d", offices)
	}
}
