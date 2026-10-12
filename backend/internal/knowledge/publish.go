package knowledge

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const abitaPracticeKey = "abita-eye-group"
const maxAbitaEntryCharacters = 800

var requiredAbitaEntries = []string{
	"current-address", "phone", "fax", "paperwork-email", "directions", "other-offices",
	"hours", "holiday-closures", "after-hours",
	"providers", "provider-languages", "scope-of-services",
	"appointment-confirmation", "new-patient-visit", "what-to-bring", "referral-requirements",
	"billing", "payments", "self-pay-pricing",
	"medical-records-requests", "prescription-copies", "social-follow-up",
}

type EntryError struct {
	EntryID string
	Rule    string
}

func (e *EntryError) Error() string { return fmt.Sprintf("Entry %q %s.", e.EntryID, e.Rule) }
func (e *EntryError) Unwrap() error { return ErrInvalidInput }

type PublishCommand struct {
	Identity           access.Identity
	PracticeID         string
	LocationID         string
	RevisionID         string
	ExpectedRevisionID string
	Reason             string
	Sections           []Section
}

func (m *Module) PublishLocation(ctx context.Context, cmd PublishCommand) (RevisionSummary, error) {
	if uuid.Validate(cmd.PracticeID) != nil || uuid.Validate(cmd.LocationID) != nil || cmd.RevisionID == uuid.Nil.String() {
		return RevisionSummary{}, ErrInvalidInput
	}
	tx, err := m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RevisionSummary{}, err
	}
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, cmd.Identity, cmd.PracticeID, cmd.LocationID)
	_ = tx.Rollback(ctx)
	if err != nil {
		return RevisionSummary{}, err
	}
	officeKey, err := m.access.ReadLocationAbitaOfficeKey(ctx, cmd.Identity, cmd.PracticeID, cmd.LocationID)
	if err != nil {
		return RevisionSummary{}, err
	}
	if authorization.Practice.Key == abitaPracticeKey {
		if err := validateAbitaStandard(cmd.Sections); err != nil {
			return RevisionSummary{}, err
		}
	}
	revision, err := m.ReplaceCorpus(ctx, ImportCommand{
		ID: cmd.RevisionID, PracticeID: cmd.PracticeID, OfficeKey: officeKey, ExpectedRevisionID: cmd.ExpectedRevisionID,
		Sections: cmd.Sections, Provenance: "portal", Reason: cmd.Reason, ActorSubject: cmd.Identity.Subject,
	})
	if err != nil {
		return RevisionSummary{}, err
	}
	return RevisionSummary{ID: revision.ID, CreatedAt: revision.CreatedAt}, nil
}

func validateAbitaStandard(sections []Section) error {
	ids := map[string]bool{}
	titles := map[string]bool{}
	for _, section := range sections {
		ids[section.ID] = true
		if titles[section.Title] {
			return &EntryError{EntryID: section.ID, Rule: "has the same title as another entry"}
		}
		titles[section.Title] = true
		if n := utf8.RuneCountInString(section.Text); n > maxAbitaEntryCharacters {
			return &EntryError{EntryID: section.ID, Rule: fmt.Sprintf("has %d characters; keep it to %d or split it", n, maxAbitaEntryCharacters)}
		}
		for _, line := range strings.Split(section.Text, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "**") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- ") {
				return &EntryError{EntryID: section.ID, Rule: "uses markdown formatting; write plain text"}
			}
		}
	}
	for _, id := range requiredAbitaEntries {
		if !ids[id] {
			return &EntryError{EntryID: id, Rule: "is required and cannot be removed"}
		}
	}
	return nil
}
