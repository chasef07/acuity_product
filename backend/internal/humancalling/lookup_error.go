package humancalling

import (
	"errors"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// callingLookupError preserves dependency failures when an authorization or
// state lookup cannot establish its result. Only a completed negative lookup,
// a missing row, or an explicit Access denial establishes the domain rejection.
func callingLookupError(err, rejection error) error {
	if err == nil || errors.Is(err, pgx.ErrNoRows) || errors.Is(err, access.ErrDenied) {
		return rejection
	}
	return err
}
