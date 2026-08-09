package access

import "testing"

func TestServiceIdentityScopesHandoffPractice(t *testing.T) {
	const (
		primaryPracticeID   = "00000000-0000-0000-0000-000000000001"
		secondaryPracticeID = "00000000-0000-0000-0000-000000000002"
	)
	identity := ServiceIdentity{
		PracticeID:                   primaryPracticeID,
		AdditionalHandoffPracticeIDs: []string{secondaryPracticeID},
	}

	primary, allowed := identity.ForHandoffPractice(primaryPracticeID)
	if !allowed || primary.PracticeID != primaryPracticeID || len(primary.AdditionalHandoffPracticeIDs) != 0 {
		t.Fatalf("primary handoff identity = %#v, allowed = %t", primary, allowed)
	}

	secondary, allowed := identity.ForHandoffPractice(secondaryPracticeID)
	if !allowed || secondary.PracticeID != secondaryPracticeID || len(secondary.AdditionalHandoffPracticeIDs) != 0 {
		t.Fatalf("secondary handoff identity = %#v, allowed = %t", secondary, allowed)
	}

	if unlisted, allowed := identity.ForHandoffPractice("00000000-0000-0000-0000-000000000003"); allowed || unlisted.PracticeID != "" {
		t.Fatalf("unlisted handoff identity = %#v, allowed = %t", unlisted, allowed)
	}
}
