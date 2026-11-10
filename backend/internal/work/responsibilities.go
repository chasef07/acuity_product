package work

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
)

type ResponsibilityProvision struct {
	PracticeKey string
	Locations   []ResponsibilityLocation
}
type ResponsibilityLocation struct {
	LocationKey string
	Members     []ResponsibilityMember
}
type ResponsibilityMember struct {
	Email    string
	Category TaskCategory
	Role     string
}
type ResponsibilityReport struct {
	Applied      int
	Unmatched    []string
	CoverageGaps []string
}

func (m *Module) ProvisionResponsibilities(ctx context.Context, input ResponsibilityProvision) (ResponsibilityReport, error) {
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ResponsibilityReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	report, err := m.ProvisionResponsibilitiesInTx(ctx, tx, input)
	if err != nil {
		return report, err
	}
	return report, tx.Commit(ctx)
}

func (m *Module) ProvisionResponsibilitiesInTx(ctx context.Context, tx pgx.Tx, input ResponsibilityProvision) (ResponsibilityReport, error) {
	report := ResponsibilityReport{Unmatched: []string{}, CoverageGaps: []string{}}
	if input.PracticeKey == "" {
		return report, ErrInvalidInput
	}
	var practiceID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key=$1`, input.PracticeKey).Scan(&practiceID); err != nil {
		return report, fmt.Errorf("resolve responsibility Practice %s: %w", input.PracticeKey, err)
	}
	locationIDs := make([]string, 0, len(input.Locations))
	for _, location := range input.Locations {
		var locationID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key=$2`, practiceID, location.LocationKey).Scan(&locationID)
		if err != nil {
			return report, fmt.Errorf("resolve responsibility Location %s: %w", location.LocationKey, err)
		}
		locationIDs = append(locationIDs, locationID)
		if _, err := tx.Exec(ctx, `INSERT INTO work_responsibility_locations VALUES($1,$2) ON CONFLICT DO NOTHING`, practiceID, locationID); err != nil {
			return report, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM work_responsibilities WHERE practice_id=$1 AND location_id=$2`, practiceID, locationID); err != nil {
			return report, err
		}
		for _, member := range location.Members {
			member.Email = strings.ToLower(strings.TrimSpace(member.Email))
			if !validTaskCategory(member.Category) || member.Category == TaskCategoryBilling || (member.Role != "primary" && member.Role != "backup") || member.Email == "" {
				return report, ErrInvalidInput
			}
			var accountEmail string
			var covered bool
			err := tx.QueryRow(ctx, `SELECT email,bool_or(covered) FROM (
 SELECT g.email, g.location_scope='ALL' OR EXISTS(SELECT 1 FROM access_grant_locations gl WHERE gl.access_grant_id=g.id AND gl.location_id=$3) AS covered
 FROM access_grants g WHERE g.practice_id=$1 AND g.email=$2 AND g.revoked_at IS NULL
 UNION ALL
 SELECT m.email, m.location_scope='ALL' OR EXISTS(SELECT 1 FROM access_membership_locations ml WHERE ml.membership_id=m.id AND ml.location_id=$3)
 FROM access_memberships m WHERE m.practice_id=$1 AND m.email=$2 AND m.revoked_at IS NULL
 ) accounts GROUP BY email`, practiceID, member.Email, locationID).Scan(&accountEmail, &covered)
			if errors.Is(err, pgx.ErrNoRows) {
				report.Unmatched = append(report.Unmatched, location.LocationKey+":"+member.Email)
				continue
			}
			if err != nil {
				return report, err
			}
			if !covered {
				report.CoverageGaps = append(report.CoverageGaps, location.LocationKey+":"+member.Email)
				continue
			}
			result, err := tx.Exec(ctx, `INSERT INTO work_responsibilities VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, practiceID, locationID, accountEmail, member.Category, member.Role)
			if err != nil {
				return report, err
			}
			report.Applied += int(result.RowsAffected())
		}
	}
	for _, table := range []string{"work_responsibilities", "work_responsibility_locations"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE practice_id=$1 AND NOT (location_id = ANY($2::uuid[]))`, practiceID, locationIDs); err != nil {
			return report, err
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return report, err
	}
	return report, nil
}
