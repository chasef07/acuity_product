package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
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
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/realtime"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/worker"
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

	revision := os.Getenv("K_REVISION")
	if revision == "" {
		revision = "local"
	}
	observer := observability.NewLogger(
		observability.RuntimeRole(config.Role),
		revision,
		slog.Default(),
	)
	pool, err := openPool(ctx, config, observer)
	if err != nil {
		return err
	}
	defer pool.Close()
	go reportMetrics(ctx, pool, observer)

	slog.Info("runtime_starting",
		"role", config.Role,
		"pool_max", config.PoolMax,
		"acquire_timeout_ms", config.AcquireTimeout.Milliseconds(),
	)
	switch config.Role {
	case app.RoleMigrate:
		return runMigrate(ctx, config, pool)
	case app.RoleWorker:
		return runWorker(ctx, config, pool, observer)
	case app.RoleProviderIngress:
		calling := humancalling.New(
			pool,
			nil,
			nil,
			humanCallingConfig(config, observer),
			nil,
		)
		attachmentStore, err := newAttachmentStore(config)
		if err != nil {
			return err
		}
		messages := messaging.New(
			pool,
			nil,
			nil,
			nil,
			messaging.Config{
				WebhookPublicKey: ed25519.PublicKey(
					config.Messaging.WebhookPublicKey,
				),
				AttachmentStore: attachmentStore,
				MediaSigningKey: config.Messaging.MediaSigningKey,
			},
			nil,
		)
		handler, err := httpapi.NewProviderIngressWithMessaging(httpapi.Config{
			AllowedOrigin:  config.BrowserOrigin,
			AcquireTimeout: config.AcquireTimeout,
			Observer:       observer,
		}, pool, calling, messages)
		if err != nil {
			return err
		}
		return serve(ctx, config, handler)
	case app.RolePortalAPI, app.RoleRealtime:
		return runAuthorizedHTTP(ctx, config, pool, observer)
	default:
		return fmt.Errorf("unsupported runtime role %q", config.Role)
	}
}

func openPool(
	ctx context.Context,
	config app.Config,
	observer observability.Observer,
) (*pgxpool.Pool, error) {
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
	poolConfig.ConnConfig.Tracer = observability.NewPoolTracer(observer)
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	return pool, nil
}

func reportMetrics(
	ctx context.Context,
	pool *pgxpool.Pool,
	observer observability.Observer,
) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	report := func() {
		stat := pool.Stat()
		observability.Record(observer, observability.DatabasePoolState(
			stat.AcquiredConns(),
			stat.IdleConns(),
			stat.MaxConns(),
		))
	}
	for {
		select {
		case <-ctx.Done():
			report()
			return
		case <-ticker.C:
			report()
		}
	}
}

func runAuthorizedHTTP(
	ctx context.Context,
	config app.Config,
	pool *pgxpool.Pool,
	observer observability.Observer,
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
			DatabaseURL:          config.DatabaseURL,
			AccessTimeout:        config.AcquireTimeout,
			HeartbeatInterval:    config.Realtime.Heartbeat,
			StreamLifetime:       config.Realtime.Lifetime,
			StreamLifetimeJitter: config.Realtime.LifetimeJitter,
			RevalidateInterval:   config.Realtime.Revalidate,
			ReconnectMin:         config.Realtime.ReconnectMin,
			ReconnectMax:         config.Realtime.ReconnectMax,
			Observer:             observer,
		}, accessModule)
		if err != nil {
			return err
		}
		go hub.Run(ctx)
		handler, err = httpapi.NewRealtime(httpapi.Config{
			AllowedOrigin:  config.BrowserOrigin,
			AcquireTimeout: config.AcquireTimeout,
			Observer:       observer,
		}, pool, httpapi.RealtimeDependencies{
			Access:        accessModule,
			Authenticator: authenticator,
			Events:        hub,
		})
		if err != nil {
			return err
		}
	} else {
		provider, err := newTelnyxProvider(config)
		if err != nil {
			return err
		}
		attachmentStore, err := newAttachmentStore(config)
		if err != nil {
			return err
		}
		callingConfig := humanCallingConfig(config, observer)
		callingConfig.VoicemailStore = attachmentStore
		calling := humancalling.New(
			pool,
			accessModule,
			provider,
			callingConfig,
			nil,
		)
		workModule := work.New(pool, accessModule, nil)
		messages := messaging.New(
			pool,
			accessModule,
			workModule,
			nil,
			messaging.Config{AttachmentStore: attachmentStore},
			nil,
		)
		serviceAuth, err := access.NewServiceAuthenticator(
			config.Service.Token,
			access.ServiceIdentity{
				Subject:       config.Service.Subject,
				PracticeID:    config.Service.PracticeID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityHumanHandoff,
					access.ServiceCapabilityCreateTask,
				},
			},
		)
		if err != nil {
			return err
		}
		handler, err = httpapi.NewPortal(httpapi.Config{
			AllowedOrigin:  config.BrowserOrigin,
			AcquireTimeout: config.AcquireTimeout,
			Observer:       observer,
		}, pool, httpapi.PortalDependencies{
			Access:               accessModule,
			Authenticator:        authenticator,
			Calling:              calling,
			Messaging:            messages,
			Work:                 workModule,
			ServiceAuthenticator: serviceAuth,
		})
		if err != nil {
			return err
		}
	}
	return serve(ctx, config, handler)
}

func runWorker(
	ctx context.Context,
	config app.Config,
	pool *pgxpool.Pool,
	observer observability.Observer,
) error {
	pingContext, cancel := context.WithTimeout(ctx, config.AcquireTimeout)
	err := pool.Ping(pingContext)
	cancel()
	if err != nil {
		return fmt.Errorf("worker database dependency: %w", err)
	}

	provider, err := newTelnyxProvider(config)
	if err != nil {
		return err
	}
	attachmentStore, err := newAttachmentStore(config)
	if err != nil {
		return err
	}
	recordingClient, err := recordingHTTPClient(
		config.HumanCalling.RecordingCAFile,
	)
	if err != nil {
		return err
	}
	callingConfig := humanCallingConfig(config, observer)
	callingConfig.VoicemailStore = attachmentStore
	callingConfig.RecordingDownloader =
		humancalling.NewHTTPRecordingDownloader(
			recordingClient,
			config.HumanCalling.RecordingAllowedHosts...,
		)
	calling := humancalling.New(
		pool,
		access.New(pool, nil),
		provider,
		callingConfig,
		nil,
	)
	messagingProvider, err := newTelnyxMessagingProvider(config)
	if err != nil {
		return err
	}
	accessModule := access.New(pool, nil)
	messages := messaging.New(
		pool,
		accessModule,
		work.New(pool, accessModule, nil),
		messagingProvider,
		messaging.Config{
			AttachmentStore:    attachmentStore,
			MediaPublicBaseURL: config.Messaging.MediaPublicBaseURL,
			MediaSigningKey:    config.Messaging.MediaSigningKey,
		},
		nil,
	)
	if err := calling.ReconcileCredentials(ctx); err != nil {
		return fmt.Errorf("initial calling credential reconciliation: %w", err)
	}
	runner, err := worker.NewWithMessaging(worker.Config{
		WorkInterval:       250 * time.Millisecond,
		WorkTimeout:        10 * time.Second,
		CredentialInterval: 30 * time.Second,
		CredentialTimeout:  config.AcquireTimeout,
		HealthInterval:     30 * time.Second,
		HealthTimeout:      config.AcquireTimeout,
		MetricInterval:     30 * time.Second,
		MetricTimeout:      config.AcquireTimeout,
		ReceiptBatchSize:   8,
		CommandBatchSize:   1,
		CommandWorkers:     2,
		ErrorBackoffMin:    250 * time.Millisecond,
		ErrorBackoffMax:    10 * time.Second,
	}, calling, messages, pool)
	if err != nil {
		return err
	}
	slog.Info("runtime_ready", "role", config.Role)
	return runner.Run(ctx)
}

func recordingHTTPClient(certificateFile string) (*http.Client, error) {
	if certificateFile == "" {
		return nil, nil
	}
	certificate, err := os.ReadFile(certificateFile)
	if err != nil {
		return nil, fmt.Errorf("read recording CA file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system recording roots: %w", err)
	}
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, errors.New("recording CA file contains no certificates")
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is unavailable")
	}
	recordingTransport := transport.Clone()
	recordingTransport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}
	return &http.Client{Transport: recordingTransport}, nil
}

func newTelnyxProvider(config app.Config) (*humancalling.TelnyxAdapter, error) {
	return humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey:  config.HumanCalling.TelnyxAPIKey,
		BaseURL: config.HumanCalling.TelnyxAPIBaseURL,
	})
}

func newTelnyxMessagingProvider(
	config app.Config,
) (*messaging.TelnyxAdapter, error) {
	return messaging.NewTelnyxAdapter(messaging.TelnyxConfig{
		APIKey:         config.HumanCalling.TelnyxAPIKey,
		BaseURL:        config.HumanCalling.TelnyxAPIBaseURL,
		WebhookBaseURL: config.Messaging.WebhookBaseURL,
	})
}

func newAttachmentStore(
	config app.Config,
) (*messaging.FileAttachmentStore, error) {
	return messaging.NewFileAttachmentStore(
		config.Messaging.AttachmentDirectory,
	)
}

func humanCallingConfig(
	config app.Config,
	observer observability.Observer,
) humancalling.Config {
	return humancalling.Config{
		HandoffSIPDomain:       config.HumanCalling.HandoffSIPDomain,
		StaffSIPDomain:         config.HumanCalling.StaffSIPDomain,
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
		PlaybackSigningKey:     config.HumanCalling.PlaybackSigningKey,
		WebhookPublicKey:       ed25519.PublicKey(config.HumanCalling.WebhookPublicKey),
		Observer:               observer,
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
	if err := migrations.ApplyRuntimeGrants(ctx, pool); err != nil {
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
	messageLocations := make([]messaging.LocationProvision, 0)
	for _, practice := range input.Practices {
		for _, location := range practice.Locations {
			if location.MessagingSender == "" &&
				location.MessagingProfileID == "" {
				continue
			}
			messageLocations = append(
				messageLocations,
				messaging.LocationProvision{
					PracticeKey:        practice.Key,
					LocationKey:        location.Key,
					Sender:             location.MessagingSender,
					MessagingProfileID: location.MessagingProfileID,
					Active:             location.MessagingActive,
				},
			)
		}
	}
	if err := messaging.New(pool, nil, nil, nil, messaging.Config{}, nil).
		Provision(ctx, messageLocations); err != nil {
		return err
	}
	voiceLocations := make([]humancalling.LocationVoiceProvision, 0)
	for _, practice := range input.Practices {
		for _, location := range practice.Locations {
			if location.VoiceNumber == "" {
				if location.VoicemailGreeting != "" {
					return fmt.Errorf(
						"Location %q has a voicemail greeting without a voice number",
						location.Key,
					)
				}
				continue
			}
			enabled := true
			if location.VoiceEnabled != nil {
				enabled = *location.VoiceEnabled
			}
			voiceLocations = append(
				voiceLocations,
				humancalling.LocationVoiceProvision{
					PracticeKey:       practice.Key,
					LocationKey:       location.Key,
					Number:            location.VoiceNumber,
					Enabled:           enabled,
					VoicemailGreeting: location.VoicemailGreeting,
				},
			)
		}
	}
	if err := humancalling.New(pool, nil, nil, humancalling.Config{}, nil).
		ProvisionLocationVoices(ctx, voiceLocations); err != nil {
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
