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
	"strings"
	"syscall"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	file := flag.String("file", "", "reviewed legacy office corpus JSON")
	source := flag.String("source", "", "Git-versioned office knowledge YAML")
	commit := flag.String("commit", "", "full Git commit SHA for source provenance")
	export := flag.Bool("export", false, "export an office revision as YAML")
	practice := flag.String("practice", "", "Practice UUID for export")
	office := flag.String("office", "", "Abita office key for export")
	revision := flag.String("revision", "", "revision UUID for export; defaults to the active revision")
	apply := flag.Bool("apply", false, "publish knowledge for an office that has none yet")
	flag.Parse()
	var err error
	if *export {
		err = runExport(*practice, *office, *revision)
	} else if *source != "" {
		if *file != "" {
			err = errors.New("use either --source or --file")
		} else {
			err = runSource(*source, *commit, *apply)
		}
	} else {
		err = run(*file, *apply)
	}
	if err != nil {
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
	return applyPublication(command, apply)
}

func applyPublication(command knowledge.ImportCommand, apply bool) error {
	if !apply {
		command.ActorSubject = "validation-only"
		if err := knowledge.ValidateImport(command); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"applied": false, "validation": "Content structure only; --apply checks the operator, the route, that the office has no knowledge yet, the Abita entry standard, and embeddings; source approval remains an operator responsibility", "practiceId": command.PracticeID, "officeKey": command.OfficeKey, "sections": len(command.Sections)})
	}
	if os.Getenv("KNOWLEDGE_IMPORT_DATABASE_URL") == "" || os.Getenv("KNOWLEDGE_OPERATOR_EMAIL") == "" || os.Getenv("KNOWLEDGE_GOOGLE_PROJECT") == "" {
		return errors.New("KNOWLEDGE_IMPORT_DATABASE_URL, KNOWLEDGE_OPERATOR_EMAIL and KNOWLEDGE_GOOGLE_PROJECT are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	command, err = preparePublication(ctx, pool, command, os.Getenv("KNOWLEDGE_OPERATOR_EMAIL"))
	if err != nil {
		return err
	}
	location := os.Getenv("KNOWLEDGE_GOOGLE_LOCATION")
	if location == "" {
		location = "us-east1"
	}
	provider, err := knowledge.NewGoogleEmbedder(ctx, os.Getenv("KNOWLEDGE_GOOGLE_PROJECT"), location, nil)
	if err != nil {
		return err
	}
	module, err := knowledge.New(pool, access.New(pool, nil), provider, knowledge.Config{})
	if err != nil {
		return err
	}
	revision, err := module.ReplaceCorpus(ctx, command)
	if err != nil {
		return fmt.Errorf("corpus import failed: %w", err)
	}
	var active string
	if err := pool.QueryRow(ctx, `SELECT revision_id::text FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2`, command.PracticeID, command.OfficeKey).Scan(&active); err != nil || active != revision.ID {
		return errors.New("publication returned but active revision verification failed")
	}
	return json.NewEncoder(os.Stdout).Encode(publicationReceipt{
		Applied: true, ActiveRevisionVerified: true,
		Revision: revision, Provenance: strings.TrimSpace(command.Provenance),
		SourceGitCommit: strings.TrimPrefix(command.Provenance, "git:"),
		PracticeID:      command.PracticeID, OfficeKey: command.OfficeKey, Sections: len(command.Sections),
	})
}

func runExport(practice, office, revision string) error {
	if os.Getenv("KNOWLEDGE_IMPORT_DATABASE_URL") == "" {
		return errors.New("KNOWLEDGE_IMPORT_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := openPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	module, err := knowledge.New(pool, access.New(pool, nil), nil, knowledge.Config{})
	if err != nil {
		return err
	}
	exported, err := exportSource(ctx, module, practice, office, revision, os.Stdout)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stderr).Encode(map[string]string{"officeKey": office, "revisionId": exported})
}

func openPool(ctx context.Context) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(os.Getenv("KNOWLEDGE_IMPORT_DATABASE_URL"))
	if err != nil {
		return nil, errors.New("invalid import database configuration")
	}
	if host := os.Getenv("KNOWLEDGE_DATABASE_HOST"); host != "" {
		if host != "127.0.0.1" {
			return nil, errors.New("KNOWLEDGE_DATABASE_HOST override only supports the loopback Cloud SQL proxy")
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
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("could not open import database")
	}

	return pool, nil
}
