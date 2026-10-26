package knowledge

import (
	"context"
	"errors"
)

func FailureCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, ErrUnavailable) {
		return "corpus_unavailable"
	}
	return "database_failure"
}
