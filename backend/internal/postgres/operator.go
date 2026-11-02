package postgres

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func OpenOperatorPool(ctx context.Context, urlEnv, hostEnv string) (*pgxpool.Pool, error) {
	config, err := operatorPoolConfig(urlEnv, hostEnv)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("could not open import database")
	}
	return pool, nil
}

func operatorPoolConfig(urlEnv, hostEnv string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(os.Getenv(urlEnv))
	if err != nil {
		return nil, errors.New("invalid import database configuration")
	}
	if host := os.Getenv(hostEnv); host != "" {
		if host != "127.0.0.1" {
			return nil, errors.New(hostEnv + " override only supports the loopback Cloud SQL proxy")
		}
		config.ConnConfig.Host = host
		config.ConnConfig.Port = 5432
		config.ConnConfig.TLSConfig = nil
		config.ConnConfig.Fallbacks = nil
	}
	config.MaxConns = 1
	config.MinConns = 0
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	return config, nil
}
