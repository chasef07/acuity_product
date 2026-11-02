package messaging

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func LoadMessage(ctx context.Context, pool *pgxpool.Pool, messageID string) (Message, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return loadMessage(ctx, tx, messageID)
}
