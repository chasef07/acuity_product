// knowledge-import replaces one complete office corpus. It is an explicit
// operator action and has no browser, agent-write, or background-worker path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	file := flag.String("file", "", "reviewed, non-patient office corpus JSON")
	apply := flag.Bool("apply", false, "atomically replace the configured office corpus")
	flag.Parse()
	if err := run(*file, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readCommand(reader io.Reader) (knowledge.ImportCommand, error) {
	var command knowledge.ImportCommand
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, errors.New("invalid corpus JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return command, errors.New("corpus must contain exactly one JSON object")
	}
	if command.ActorSubject != "" {
		return command, errors.New("actorSubject must be omitted; the bound operator supplies attribution")
	}
	return command, nil
}

func run(path string, apply bool) error {
	if path == "" {
		return errors.New("--file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("could not open corpus file")
	}
	defer file.Close()
	command, err := readCommand(file)
	if err != nil {
		return err
	}
	if !apply {
		command.ActorSubject = "validation-only"
		if err := knowledge.ValidateImport(command); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"applied": false, "validation": "Content structure only; --apply checks operator, route, revision and embeddings; source approval remains an operator responsibility", "officeKey": command.OfficeKey, "sections": len(command.Sections), "expectedRevisionId": command.ExpectedRevisionID})
	}
	if os.Getenv("KNOWLEDGE_IMPORT_DATABASE_URL") == "" || os.Getenv("KNOWLEDGE_OPERATOR_EMAIL") == "" || os.Getenv("KNOWLEDGE_GOOGLE_PROJECT") == "" {
		return errors.New("KNOWLEDGE_IMPORT_DATABASE_URL, KNOWLEDGE_OPERATOR_EMAIL and KNOWLEDGE_GOOGLE_PROJECT are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	config, err := pgxpool.ParseConfig(os.Getenv("KNOWLEDGE_IMPORT_DATABASE_URL"))
	if err != nil {
		return errors.New("invalid import database configuration")
	}
	config.MaxConns = 1
	config.MinConns = 0
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("could not open import database")
	}
	defer pool.Close()
	if err := pool.QueryRow(ctx, `SELECT user_subject FROM access_platform_operators WHERE email=$1 AND user_subject IS NOT NULL`, os.Getenv("KNOWLEDGE_OPERATOR_EMAIL")).Scan(&command.ActorSubject); err != nil {
		return errors.New("import requires an existing bound Platform Operator")
	}
	location := os.Getenv("KNOWLEDGE_GOOGLE_LOCATION")
	if location == "" {
		location = "us-east1"
	}
	provider, err := knowledge.NewGoogleEmbedder(ctx, os.Getenv("KNOWLEDGE_GOOGLE_PROJECT"), location, nil)
	if err != nil {
		return err
	}
	module, err := knowledge.New(pool, access.New(pool, nil), provider, knowledge.Config{ProviderTimeout: 30 * time.Second})
	if err != nil {
		return err
	}
	revision, err := module.ReplaceCorpus(ctx, command)
	if err != nil {
		return fmt.Errorf("corpus import failed: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"applied": true, "revision": revision, "officeKey": command.OfficeKey, "sections": len(command.Sections)})
}
