package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/app"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/realtime"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if err := run(); err != nil {
		logger.Error("runtime_stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := app.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	pool, err := openPool(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()

	slog.Info("runtime_starting",
		"role", config.Role,
		"pool_max", config.PoolMax,
		"acquire_timeout_ms", config.AcquireTimeout.Milliseconds(),
	)
	switch config.Role {
	case app.RoleMigrate:
		return runMigrate(ctx, config, pool)
	case app.RoleWorker:
		return runWorker(ctx, config, pool)
	case app.RoleProviderIngress:
		calling := humancalling.New(
			pool,
			nil,
			nil,
			humanCallingConfig(config),
			nil,
		)
		handler, err := httpapi.NewWithCalling(httpapi.Config{
			Role:           string(config.Role),
			AllowedOrigin:  config.BrowserOrigin,
			AcquireTimeout: config.AcquireTimeout,
		}, pool, nil, nil, calling, nil)
		if err != nil {
			return err
		}
		return serve(ctx, config, handler)
	case app.RolePortalAPI, app.RoleRealtime:
		return runAuthorizedHTTP(ctx, config, pool)
	default:
		return fmt.Errorf("unsupported runtime role %q", config.Role)
	}
}

func openPool(ctx context.Context, config app.Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfig.MaxConns = config.PoolMax
	poolConfig.MinConns = 0
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnLifetimeJitter = 5 * time.Minute
	poolConfig.ConnConfig.ConnectTimeout = config.AcquireTimeout
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	return pool, nil
}

func runAuthorizedHTTP(
	ctx context.Context,
	config app.Config,
	pool *pgxpool.Pool,
) error {
	accessModule := access.New(pool, nil)
	authenticator, err := authn.NewJWKSAuthenticator(authn.JWKSConfig{
		URL:      config.JWKSURL,
		Issuer:   config.AuthIssuer,
		Audience: config.APIAudience,
	})
	if err != nil {
		return err
	}

	var handler http.Handler
	if config.Role == app.RoleRealtime {
		hub, err := realtime.New(realtime.Config{
			DatabaseURL:        config.DatabaseURL,
			AccessTimeout:      config.AcquireTimeout,
			HeartbeatInterval:  config.Realtime.Heartbeat,
			StreamLifetime:     config.Realtime.Lifetime,
			RevalidateInterval: config.Realtime.Revalidate,
			ReconnectMin:       config.Realtime.ReconnectMin,
			ReconnectMax:       config.Realtime.ReconnectMax,
		}, accessModule)
		if err != nil {
			return err
		}
		go hub.Run(ctx)
		handler, err = httpapi.NewWithEvents(httpapi.Config{
			Role:           string(config.Role),
			AllowedOrigin:  config.BrowserOrigin,
			AcquireTimeout: config.AcquireTimeout,
		}, pool, accessModule, authenticator, hub)
		if err != nil {
			return err
		}
	} else {
		provider, err := newTelnyxProvider(config)
		if err != nil {
			return err
		}
		calling := humancalling.New(
			pool,
			accessModule,
			provider,
			humanCallingConfig(config),
			nil,
		)
		serviceAuth, err := humancalling.NewServiceAuthenticator(
			config.HumanCalling.HandoffServiceToken,
			humancalling.ServiceIdentity{
				Subject:    config.HumanCalling.HandoffServiceSubject,
				PracticeID: config.HumanCalling.HandoffServicePractice,
			},
		)
		if err != nil {
			return err
		}
		handler, err = httpapi.NewWithCalling(httpapi.Config{
			Role:           string(config.Role),
			AllowedOrigin:  config.BrowserOrigin,
			AcquireTimeout: config.AcquireTimeout,
		}, pool, accessModule, authenticator, calling, serviceAuth)
		if err != nil {
			return err
		}
	}
	return serve(ctx, config, handler)
}

func runWorker(ctx context.Context, config app.Config, pool *pgxpool.Pool) error {
	pingContext, cancel := context.WithTimeout(ctx, config.AcquireTimeout)
	err := pool.Ping(pingContext)
	cancel()
	if err != nil {
		return fmt.Errorf("worker database dependency: %w", err)
	}
	slog.Info("runtime_ready", "role", config.Role)

	provider, err := newTelnyxProvider(config)
	if err != nil {
		return err
	}
	calling := humancalling.New(
		pool,
		access.New(pool, nil),
		provider,
		humanCallingConfig(config),
		nil,
	)
	if err := calling.ReconcileCredentials(ctx); err != nil {
		return fmt.Errorf("initial calling credential reconciliation: %w", err)
	}

	workTicker := time.NewTicker(250 * time.Millisecond)
	defer workTicker.Stop()
	credentialTicker := time.NewTicker(30 * time.Second)
	defer credentialTicker.Stop()
	healthTicker := time.NewTicker(30 * time.Second)
	defer healthTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-workTicker.C:
			workContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			if _, err := calling.ExpireOffers(workContext); err != nil {
				slog.Warn("calling_offer_expiry_failed", "error", err)
			}
			if _, err := calling.ExpireConnections(workContext); err != nil {
				slog.Warn("calling_connection_expiry_failed", "error", err)
			}
			if err := calling.RecoverInterruptedCommands(workContext); err != nil {
				slog.Warn("provider_command_recovery_failed", "error", err)
			}
			if _, err := calling.ReconcileConfirmedHangups(workContext); err != nil {
				slog.Warn("provider_hangup_reconciliation_failed", "error", err)
			}
			if _, err := calling.ProcessNextReceipt(workContext); err != nil {
				slog.Warn("provider_receipt_processing_failed", "error", err)
			}
			if _, err := calling.ProcessNextCredentialReconciliation(workContext); err != nil {
				slog.Warn("provider_credential_reconciliation_failed", "error", err)
			}
			if _, err := calling.ProcessNextCommand(workContext); err != nil {
				slog.Warn("provider_command_processing_failed", "error", err)
			}
			cancel()
		case <-credentialTicker.C:
			workContext, cancel := context.WithTimeout(ctx, config.AcquireTimeout)
			if err := calling.ReconcileCredentials(workContext); err != nil {
				slog.Warn("calling_credential_reconciliation_failed", "error", err)
			}
			cancel()
		case <-healthTicker.C:
			pingContext, cancel := context.WithTimeout(ctx, config.AcquireTimeout)
			err := pool.Ping(pingContext)
			cancel()
			if err != nil {
				slog.Warn("worker_dependency_unavailable")
			}
		}
	}
}

func newTelnyxProvider(config app.Config) (*humancalling.TelnyxAdapter, error) {
	return humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:                 config.HumanCalling.TelnyxAPIKey,
		BaseURL:                config.HumanCalling.TelnyxAPIBaseURL,
		CallControlID:          config.HumanCalling.CallControlID,
		CredentialConnectionID: config.HumanCalling.CredentialConnectionID,
		FromNumber:             config.HumanCalling.FromNumber,
		RingbackURL:            config.HumanCalling.RingbackURL,
	})
}

func humanCallingConfig(config app.Config) humancalling.Config {
	return humancalling.Config{
		SIPDomain:              config.HumanCalling.SIPDomain,
		OfferDuration:          config.HumanCalling.OfferDuration,
		ConnectionTimeout:      config.HumanCalling.ConnectionTimeout,
		HandoffTokenKey:        config.HumanCalling.HandoffTokenKey,
		LeaseDuration:          config.HumanCalling.LeaseDuration,
		ReadinessGrace:         config.HumanCalling.ReadinessGrace,
		CallControlID:          config.HumanCalling.CallControlID,
		CredentialConnectionID: config.HumanCalling.CredentialConnectionID,
		FromNumber:             config.HumanCalling.FromNumber,
		RingbackURL:            config.HumanCalling.RingbackURL,
		RecordingBucket:        config.HumanCalling.RecordingBucket,
		WebhookPublicKey:       ed25519.PublicKey(config.HumanCalling.WebhookPublicKey),
	}
}

func runMigrate(
	ctx context.Context,
	config app.Config,
	pool *pgxpool.Pool,
) error {
	if err := migrations.Apply(ctx, pool); err != nil {
		return err
	}
	if config.ProvisioningInput == "" {
		slog.Info("migrations_applied", "provisioning", false)
		return nil
	}

	inputFile, err := os.Open(config.ProvisioningInput)
	if err != nil {
		return fmt.Errorf("open provisioning input: %w", err)
	}
	defer inputFile.Close()
	var input access.Provisioning
	decoder := json.NewDecoder(inputFile)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode provisioning input: %w", err)
	}
	output, err := os.OpenFile(
		config.ProvisioningOutput,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("create provisioning output: %w", err)
	}
	keepOutput := false
	defer func() {
		_ = output.Close()
		if !keepOutput {
			_ = os.Remove(config.ProvisioningOutput)
		}
	}()
	provisioned, err := access.New(pool, nil).Provision(ctx, input)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(provisioned); err != nil {
		return fmt.Errorf("write provisioning output: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close provisioning output: %w", err)
	}
	keepOutput = true
	slog.Info("migrations_applied",
		"provisioning", true,
		"invitation_count", len(provisioned.Invitations),
	)
	return nil
}

func serve(ctx context.Context, config app.Config, handler http.Handler) error {
	server := &http.Server{
		Addr:              ":" + strconv.Itoa(config.HTTPPort),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if config.Role == app.RoleRealtime {
		server.WriteTimeout = 0
	}
	errorChannel := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorChannel <- err
			return
		}
		errorChannel <- nil
	}()
	slog.Info("runtime_ready", "role", config.Role, "port", config.HTTPPort)

	select {
	case err := <-errorChannel:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	}
}
