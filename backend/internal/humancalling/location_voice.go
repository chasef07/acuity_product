package humancalling

import (
	"context"
	"fmt"
)

func (m *Module) CallingNumbersForLocations(
	ctx context.Context,
	practiceID string,
	locationIDs []string,
) (map[string]string, error) {
	numbers := make(map[string]string, len(locationIDs))
	if len(locationIDs) == 0 {
		return numbers, nil
	}
	rows, err := m.pool.Query(ctx, `
		SELECT location_id::text, min(phone), count(*)
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1 AND location_id = ANY($2::uuid[]) AND enabled
		GROUP BY location_id
	`, practiceID, locationIDs)
	if err != nil {
		return nil, fmt.Errorf("load Location calling numbers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var locationID, phone string
		var count int
		if err := rows.Scan(&locationID, &phone, &count); err != nil {
			return nil, fmt.Errorf("scan Location calling number: %w", err)
		}
		if count != 1 {
			return nil, fmt.Errorf(
				"Location %s has %d enabled calling numbers",
				locationID,
				count,
			)
		}
		numbers[locationID] = phone
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Location calling numbers: %w", err)
	}
	return numbers, nil
}
