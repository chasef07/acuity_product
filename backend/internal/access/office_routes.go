package access

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (m *Module) LocationHasAbitaOfficeRoute(ctx context.Context, tx pgx.Tx, practiceID, locationID, officeKey string) (bool, error) {
	var routed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM access_abita_office_locations
			WHERE practice_id = $1
				AND location_id = $2
				AND office_key = $3
		)
	`, practiceID, locationID, officeKey).Scan(&routed); err != nil {
		return false, fmt.Errorf("read Abita Office Route: %w", err)
	}
	return routed, nil
}

type AbitaOfficeRoute struct {
	OfficeKey    string
	LocationID   string
	LocationName string
}

func (m *Module) ReadAbitaOfficeRoutes(ctx context.Context, tx pgx.Tx, practiceID string) ([]AbitaOfficeRoute, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.office_key, r.location_id::text, l.name
		FROM access_abita_office_locations r
		JOIN access_locations l ON l.practice_id = r.practice_id AND l.id = r.location_id
		WHERE r.practice_id = $1
		ORDER BY r.office_key
	`, practiceID)
	if err != nil {
		return nil, fmt.Errorf("read Abita Office Routes: %w", err)
	}
	routes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[AbitaOfficeRoute])
	if err != nil {
		return nil, fmt.Errorf("scan Abita Office Routes: %w", err)
	}
	return routes, nil
}
