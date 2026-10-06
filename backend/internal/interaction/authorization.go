package interaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type audience int

const (
	audienceStaff audience = iota
	audienceAdmin
	audienceOperator
)

func (who audience) permits(authorization access.Authorization) bool {
	switch who {
	case audienceOperator:
		return authorization.PlatformOperator
	case audienceAdmin:
		return authorization.PlatformOperator || authorization.Membership.Role == access.RoleAdmin
	default:
		return true
	}
}

func (m *Module) available() error {
	if m.database == nil || m.access == nil {
		return errUnavailable
	}
	return nil
}

func (m *Module) authorize(
	ctx context.Context,
	tx pgx.Tx,
	identity access.Identity,
	practiceID string,
	locationID string,
	mutation bool,
	who audience,
) (access.Authorization, error) {
	lock := m.access.LockReadAuthorization
	if mutation {
		lock = m.access.LockMutationAuthorization
	}
	authorization, err := lock(ctx, tx, identity, practiceID, locationID)
	if errors.Is(err, access.ErrDenied) || (err == nil && !who.permits(authorization)) {
		return access.Authorization{}, ErrDenied
	}
	if err != nil {
		return access.Authorization{}, fmt.Errorf("authorize AI Interaction access: %w", err)
	}
	return authorization, nil
}

func (m *Module) beginInteractionAccess(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
	mutation bool,
	who audience,
) (pgx.Tx, access.Authorization, error) {
	if err := m.available(); err != nil {
		return nil, access.Authorization{}, err
	}
	if !validUUID(interactionID) {
		return nil, access.Authorization{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, access.Authorization{}, fmt.Errorf("begin AI Interaction access: %w", err)
	}
	var practiceID, locationID string
	err = tx.QueryRow(ctx, `SELECT practice_id::text, location_id::text FROM ai_interactions WHERE id = $1`, interactionID).Scan(&practiceID, &locationID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	} else if err != nil {
		err = fmt.Errorf("locate AI Interaction: %w", err)
	}
	var authorization access.Authorization
	if err == nil {
		authorization, err = m.authorize(ctx, tx, identity, practiceID, locationID, mutation, who)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, access.Authorization{}, err
	}
	return tx, authorization, nil
}

func readInteraction(ctx context.Context, tx pgx.Tx, interactionID string) (Interaction, error) {
	stored, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+` WHERE interaction.id = $1`, interactionID))
	if err != nil {
		return Interaction{}, fmt.Errorf("read AI Interaction: %w", err)
	}
	return stored, nil
}

const (
	analyticsRowLimit        = 50000
	analyticsReviewScanLimit = 2000
)

type analyticsScope struct {
	tx          pgx.Tx
	locationIDs []string
}

func (m *Module) beginAnalyticsScope(
	ctx context.Context,
	identity access.Identity,
	practiceID string,
	locationID string,
	who audience,
) (analyticsScope, error) {
	if err := m.available(); err != nil {
		return analyticsScope{}, err
	}
	if !validUUID(practiceID) || (locationID != "" && !validUUID(locationID)) {
		return analyticsScope{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return analyticsScope{}, fmt.Errorf("begin AI analytics: %w", err)
	}
	scope := analyticsScope{tx: tx}
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout = '1500ms'; SET LOCAL lock_timeout = '100ms'; SET LOCAL max_parallel_workers_per_gather = 0; SET LOCAL work_mem = '4MB'`); err != nil {
		err = fmt.Errorf("bound AI analytics: %w", err)
	}
	var authorization access.Authorization
	if err == nil {
		authorization, err = m.authorize(ctx, tx, identity, practiceID, locationID, false, who)
	}
	if err == nil {
		scope.locationIDs = authorizedLocationIDs(authorization, locationID)
		if len(scope.locationIDs) == 0 {
			err = ErrDenied
		}
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return analyticsScope{}, err
	}
	return scope, nil
}

func reportingZone(name string) (*time.Location, bool) {
	if strings.TrimSpace(name) == "" || name == "Local" {
		return nil, false
	}
	zone, err := time.LoadLocation(name)
	return zone, err == nil
}
