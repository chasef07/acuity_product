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
