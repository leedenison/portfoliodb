package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"buf.build/go/protovalidate"
	"github.com/jmoiron/sqlx"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/auth/allowlist"
	"github.com/leedenison/portfoliodb/server/auth/google"
	"github.com/leedenison/portfoliodb/server/auth/session"
	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/migrate"
	"github.com/leedenison/portfoliodb/server/db/postgres"
	"github.com/leedenison/portfoliodb/server/grouping"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/candidate"
	"github.com/leedenison/portfoliodb/server/inflationfetcher"
	"github.com/leedenison/portfoliodb/server/logger"
	"github.com/leedenison/portfoliodb/server/migrations"
	cashcand "github.com/leedenison/portfoliodb/server/plugins/cash/candidate"
	cashid "github.com/leedenison/portfoliodb/server/plugins/cash/identifier"
	eodhdce "github.com/leedenison/portfoliodb/server/plugins/eodhd/corporateevents"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
	eodhdplugin "github.com/leedenison/portfoliodb/server/plugins/eodhd/identifier"
	eodhdprice "github.com/leedenison/portfoliodb/server/plugins/eodhd/price"
	massivece "github.com/leedenison/portfoliodb/server/plugins/massive/corporateevents"
	massiveplugin "github.com/leedenison/portfoliodb/server/plugins/massive/identifier"
	massiveprice "github.com/leedenison/portfoliodb/server/plugins/massive/price"
	onsinflation "github.com/leedenison/portfoliodb/server/plugins/ons/inflation"
	openaicand "github.com/leedenison/portfoliodb/server/plugins/openai/candidate"
	openfigiexchmap "github.com/leedenison/portfoliodb/server/plugins/openfigi/exchangemap"
	openfigiplugin "github.com/leedenison/portfoliodb/server/plugins/openfigi/identifier"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/service/api"
	authservice "github.com/leedenison/portfoliodb/server/service/auth"
	"github.com/leedenison/portfoliodb/server/service/ingestion"
	"github.com/leedenison/portfoliodb/server/transfermatch"
	"github.com/leedenison/portfoliodb/server/validate"
	"github.com/leedenison/portfoliodb/server/worker"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Set via -ldflags at build time.
var buildRevision = "dev"

// defaultDBMaxConns bounds the connection pool. See where it is applied for how
// it is arrived at -- it is a memory budget as much as a handle count.
const defaultDBMaxConns = 16

// defaultTelemetryDBMaxConns bounds the telemetry pool, which is separate from the
// application's because telemetry must never join the work's transaction. Its
// writes are single-row inserts with no sort nodes, so unlike the application pool
// it costs handles and not work_mem, and a handful is enough for the few runs in
// flight at once. It is small because both pools draw on the same
// max_connections -- 25 in the dev stack -- and Grafana will want some too.
const defaultTelemetryDBMaxConns = 4

func main() {
	grpcAddr := flag.String("grpc-addr", envOrDefault("PORTFOLIODB_GRPC_ADDR", ":50051"), "gRPC listen address")
	dbURL := flag.String("db-url", os.Getenv("PORTFOLIODB_DB_URL"), "PostgreSQL connection URL")
	redisURL := flag.String("redis-url", envOrDefault("PORTFOLIODB_REDIS_URL", os.Getenv("REDIS_URL")), "Redis connection URL for sessions")
	dbMaxConns := flag.Int("db-max-conns", parseInt(os.Getenv("PORTFOLIODB_DB_MAX_CONNS"), defaultDBMaxConns), "maximum open database connections")
	telemetryDBMaxConns := flag.Int("telemetry-db-max-conns", parseInt(os.Getenv("PORTFOLIODB_TELEMETRY_DB_MAX_CONNS"), defaultTelemetryDBMaxConns), "maximum open telemetry database connections")
	flag.Parse()
	if *dbURL == "" {
		log.Fatal("PORTFOLIODB_DB_URL or -db-url required")
	}
	if *redisURL == "" {
		log.Fatal("PORTFOLIODB_REDIS_URL or REDIS_URL required")
	}
	rawConn, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer rawConn.Close()
	// database/sql leaves the open connection count unlimited, so without this the
	// only ceiling is postgres's max_connections -- which the workers, the API and
	// migrations all draw from the same pool against. That matters beyond running
	// out of handles: work_mem is charged per sort node per connection, and the
	// valuation query holds four at once, so the server's real memory exposure is
	// conns * 4 * work_mem. At the default 16 against the pinned 16MB that is a
	// 1GB ceiling, which fits the dev stack's 2g postgres alongside its 512MB of
	// shared buffers, and stays under its max_connections of 25 with room for a
	// psql session. Raise both together or neither.
	//
	// Telemetry adds a second pool of its own further down, so the handle budget is
	// the sum of the two -- 20 of the dev stack's 25 at the defaults. The memory
	// budget is unchanged: telemetry writes single rows and sorts nothing.
	rawConn.SetMaxOpenConns(*dbMaxConns)
	// Idle matched to open so a busy period does not reconnect on every request,
	// with an idle timeout so a quiet one gives the backends back.
	rawConn.SetMaxIdleConns(*dbMaxConns)
	rawConn.SetConnMaxIdleTime(5 * time.Minute)
	if err := rawConn.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, rawConn, migrations.Files); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	conn := sqlx.NewDb(rawConn, "postgres")
	// The engine is wired into the store, so an upload's postings are partitioned in
	// the transaction that writes them rather than in whatever shape they arrived.
	database := postgres.New(conn).WithSettler(grouping.NewEngine())

	// A second pool, for telemetry alone. It is separate rather than shared because
	// telemetry must not join the work's transaction: a failed import rolls back,
	// and telemetry riding along would erase the diagnostics for the run most worth
	// inspecting. The two pools add up against one max_connections, so raising
	// either means checking the sum -- see the comment on the cap above.
	telemetryConn, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("telemetry db open: %v", err)
	}
	defer telemetryConn.Close()
	telemetryConn.SetMaxOpenConns(*telemetryDBMaxConns)
	telemetryConn.SetMaxIdleConns(*telemetryDBMaxConns)
	telemetryConn.SetConnMaxIdleTime(5 * time.Minute)

	// Redis session store
	ropt, err := redis.ParseURL(*redisURL)
	if err != nil {
		log.Fatalf("redis url: %v", err)
	}
	rdb := redis.NewClient(ropt)
	defer rdb.Close()
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}
	sessionTTL := 30 * 24 * time.Hour
	extendTTL := 72 * time.Hour
	sessionStore := session.NewRedisStore(rdb, "portfoliodb:session:", extendTTL)

	inner := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	})
	logLevelEnv := envOrDefault("LOG_LEVEL", "debug")
	h := logger.NewHandler(inner, logLevelEnv)
	slog.SetDefault(slog.New(h))
	serverLogger := slog.Default()
	serverLogger.Info("LOG_LEVEL configured", "levels", logger.Summary(logLevelEnv))

	telemetryDB := postgres.NewTelemetry(sqlx.NewDb(telemetryConn, "postgres"),
		logger.WithCategory(serverLogger, "server/db/telemetry"))
	// The store records the merges it decides, which is the one telemetry row no
	// caller can write: which instruments an identifier set landed on, and whether
	// a claim admitted joining them, is settled inside one transaction and is
	// invisible from above.
	database.WithTelemetry(telemetryDB)

	// Google ID token verifier
	googleClientID := os.Getenv("GOOGLE_OAUTH_CLIENT_ID")
	if googleClientID == "" {
		log.Fatal("GOOGLE_OAUTH_CLIENT_ID required")
	}
	jwksTTL := parseDuration(os.Getenv("GOOGLE_JWKS_CACHE_TTL"), google.DefaultJWKSCacheTTL)
	clockSkew := parseDuration(os.Getenv("GOOGLE_TOKEN_CLOCK_SKEW"), google.DefaultClockSkew)
	verifierOpts := []google.VerifierOption{
		google.WithJWKSCacheTTL(jwksTTL),
		google.WithClockSkew(clockSkew),
	}
	if cliClientID := os.Getenv("CLI_OAUTH_CLIENT_ID"); cliClientID != "" {
		verifierOpts = append(verifierOpts, google.WithAdditionalClientIDs(cliClientID))
	}
	verifier := google.NewVerifier(googleClientID, verifierOpts...)

	// Allowlist for Auth
	var allowlistMatcher *allowlist.Matcher
	if patterns := parseAllowlist(os.Getenv("ACCOUNT_CREATE_EMAIL_ALLOWLIST")); len(patterns) > 0 {
		mode := allowlist.ModeGlob
		if os.Getenv("ACCOUNT_CREATE_ALLOWLIST_MODE") == "regex" {
			mode = allowlist.ModeRegex
		}
		caseSensitive := os.Getenv("ACCOUNT_CREATE_ALLOWLIST_CASE_SENSITIVE") == "true" || os.Getenv("ACCOUNT_CREATE_ALLOWLIST_CASE_SENSITIVE") == "1"
		var err error
		allowlistMatcher, err = allowlist.NewMatcher(patterns, mode, caseSensitive)
		if err != nil {
			log.Fatalf("allowlist: %v", err)
		}
	}

	cookieName := envOrDefault("PORTFOLIODB_SESSION_COOKIE", "portfoliodb_session")
	cookieSecure := os.Getenv("PORTFOLIODB_COOKIE_SECURE") != "" && os.Getenv("PORTFOLIODB_COOKIE_SECURE") != "0" && strings.ToLower(os.Getenv("PORTFOLIODB_COOKIE_SECURE")) != "false"
	cookieMaxAge := 30 * 24 * 3600 // 30 days in seconds
	machineSessionTTL := parseDuration(os.Getenv("MACHINE_SESSION_TTL"), time.Hour)
	authServer := authservice.NewServer(
		verifier,
		sessionStore,
		database,
		database,
		allowlistMatcher,
		authservice.CookieConfig{
			Name:     cookieName,
			Path:     "/",
			MaxAge:   cookieMaxAge,
			Secure:   cookieSecure,
			SameSite: "Lax",
		},
		sessionTTL,
		extendTTL,
		machineSessionTTL,
		os.Getenv("ADMIN_AUTH_SUB"),
	)

	interceptorConfig := auth.InterceptorConfig{
		SkipAuthPrefixes: append([]string{"/grpc.reflection.", "/grpc.health.v1."}, e2eSkipPrefixes()...),
		NoSessionMethods: []string{
			"/portfoliodb.auth.v1.AuthService/AuthUser",
			"/portfoliodb.auth.v1.AuthService/AuthMachine",
		},
		OptionalSessionMethods: []string{"/portfoliodb.auth.v1.AuthService/Logout"},
		SessionStore:           sessionStore,
		SessionCookieName:      cookieName,
		ExtendTTL:              extendTTL,
	}

	pluginHTTPClient := newPluginHTTPClient()
	pluginRegistry := identifier.NewRegistry()
	openfigiExchMap := openfigiexchmap.New()
	pluginRegistry.Register(openfigiplugin.PluginID, openfigiplugin.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/openfigi"), pluginHTTPClient, openfigiExchMap))
	pluginRegistry.Register(massiveplugin.PluginID, massiveplugin.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/massive"), pluginHTTPClient, nil))
	exchMap := exchangemap.New()
	eodhdPlugin := eodhdplugin.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/eodhd"), pluginHTTPClient, exchMap)
	pluginRegistry.Register(eodhdplugin.PluginID, eodhdPlugin)
	pluginRegistry.Register(cashid.PluginID, cashid.NewPlugin(database))
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryIdentifier, pluginRegistry.ListIDs(), func(id string) []byte {
		if p := pluginRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure identifier plugin configs: %v", err)
	}
	candRegistry := candidate.NewRegistry()
	candRegistry.Register(openaicand.PluginID, openaicand.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/openai"), newCandidateHTTPClient()))
	candRegistry.Register(cashcand.PluginID, cashcand.NewPlugin())
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryCandidate, candRegistry.ListIDs(), func(id string) []byte {
		if p := candRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure candidate plugin configs: %v", err)
	}
	priceRegistry := pricefetcher.NewRegistry()
	priceRegistry.Register(massiveprice.PluginID, massiveprice.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/massive/price"), pluginHTTPClient))
	priceRegistry.Register(eodhdprice.PluginID, eodhdprice.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/eodhd/price"), pluginHTTPClient, exchMap))
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryPrice, priceRegistry.ListIDs(), func(id string) []byte {
		if p := priceRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure price plugin configs: %v", err)
	}
	inflationRegistry := inflationfetcher.NewRegistry()
	inflationRegistry.Register(onsinflation.PluginID, onsinflation.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/ons/inflation"), pluginHTTPClient))
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryInflation, inflationRegistry.ListIDs(), func(id string) []byte {
		if p := inflationRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure inflation plugin configs: %v", err)
	}
	corporateEventRegistry := corporateevents.NewRegistry()
	corporateEventRegistry.Register(massivece.PluginID, massivece.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/massive/corporateevents"), pluginHTTPClient))
	corporateEventRegistry.Register(eodhdce.PluginID, eodhdce.NewPlugin(logger.WithCategory(serverLogger, "server/plugins/eodhd/corporateevents"), pluginHTTPClient, exchMap))
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryCorporateEvent, corporateEventRegistry.ListIDs(), func(id string) []byte {
		if p := corporateEventRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure corporate event plugin configs: %v", err)
	}
	inflationTrigger := make(chan struct{}, 1)
	priceTrigger := make(chan struct{}, 1)
	corporateEventTrigger := make(chan struct{}, 1)
	transferMatchTrigger := make(chan struct{}, 1)
	groupingTrigger := make(chan struct{}, 1)
	queue := make(chan *ingestion.JobRequest, 256)
	workers := worker.NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingestionLogger := logger.WithCategory(serverLogger, "server/service/ingestion")
	go ingestion.RunWorker(ctx, ingestion.WorkerOptions{
		DB:                    database,
		Queue:                 queue,
		IdentifierRegistry:    pluginRegistry,
		CandidateRegistry:     candRegistry,
		TelemetryDB:           telemetryDB,
		Logger:                ingestionLogger,
		PriceTrigger:          priceTrigger,
		CorporateEventTrigger: corporateEventTrigger,
		TransferMatchTrigger:  transferMatchTrigger,
		GroupingTrigger:       groupingTrigger,
		Workers:               workers,
	})
	go pricefetcher.RunWorker(ctx, database, priceRegistry, telemetryDB, logger.WithCategory(serverLogger, "server/pricefetcher"), priceTrigger, workers)
	go inflationfetcher.RunWorker(ctx, database, inflationRegistry, telemetryDB, logger.WithCategory(serverLogger, "server/inflationfetcher"), inflationTrigger, workers)
	go corporateevents.RunWorker(ctx, database, corporateEventRegistry, telemetryDB, logger.WithCategory(serverLogger, "server/corporateevents"), corporateEventTrigger, workers)
	go transfermatch.RunWorker(ctx, database, telemetryDB, logger.WithCategory(serverLogger, "server/transfermatch"), transferMatchTrigger, workers)
	go grouping.RunWorker(ctx, database, telemetryDB, logger.WithCategory(serverLogger, "server/grouping"), groupingTrigger, workers)
	// Stamp the runs a previous process left unfinished. This has to happen before
	// the re-enqueue below, which opens runs of its own that must not be swept, and
	// it is what lets a run with no outcome mean one running now.
	if swept, err := telemetryDB.SweepIncompleteRuns(ctx); err != nil {
		log.Printf("telemetry: sweep incomplete runs: %v", err)
	} else if swept > 0 {
		log.Printf("telemetry: stamped %d run(s) incomplete", swept)
	}
	// Re-enqueue incomplete jobs from a previous run.
	if pending, err := database.ListPendingJobs(ctx); err == nil {
		for _, p := range pending {
			select {
			case queue <- &ingestion.JobRequest{JobID: p.ID, JobType: p.JobType}:
				log.Printf("re-enqueued %s job %s", p.JobType, p.ID)
			default:
				log.Printf("queue full, skipping re-enqueue of job %s", p.ID)
			}
		}
	}
	enqueueJob := func(jobID, jobType string) error {
		select {
		case queue <- &ingestion.JobRequest{JobID: jobID, JobType: jobType}:
			return nil
		default:
			return fmt.Errorf("job queue full")
		}
	}
	validator, err := protovalidate.New()
	if err != nil {
		log.Fatalf("protovalidate.New: %v", err)
	}
	svc := grpc.NewServer(
		grpc.MaxRecvMsgSize(32<<20),
		grpc.ChainUnaryInterceptor(
			logger.UnaryErrorInterceptor(serverLogger),
			auth.UnaryInterceptor(interceptorConfig),
			validate.UnaryInterceptor(validator),
		),
		grpc.ChainStreamInterceptor(
			logger.StreamErrorInterceptor(serverLogger),
			auth.StreamInterceptor(interceptorConfig),
		),
	)
	authv1.RegisterAuthServiceServer(svc, authServer)
	apiv1.RegisterApiServiceServer(svc, api.NewServer(api.ServerConfig{
		DB:                     database,
		TelemetryDB:            telemetryDB,
		PluginRegistry:         pluginRegistry,
		CandidateRegistry:      candRegistry,
		PriceRegistry:          priceRegistry,
		PriceTrigger:           priceTrigger,
		InflationRegistry:      inflationRegistry,
		InflationTrigger:       inflationTrigger,
		CorporateEventRegistry: corporateEventRegistry,
		CorporateEventTrigger:  corporateEventTrigger,
		TransferMatchTrigger:   transferMatchTrigger,
		GroupingTrigger:        groupingTrigger,
		WorkerRegistry:         workers,
		EnqueueJob:             api.JobEnqueuer(enqueueJob),
	}))
	ingestionv1.RegisterIngestionServiceServer(svc, ingestion.NewServer(database, queue))
	reflection.Register(svc)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(svc, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	registerE2EService(svc)
	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
		svc.GracefulStop()
	}()
	log.Printf("build: %s", buildRevision)
	log.Printf("listening on %s", *grpcAddr)
	if err := svc.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
	stopE2ERecorder()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseInt reads a positive integer setting, falling back on anything it cannot
// use: an unset variable, a non-number, or a zero or negative count, since zero
// means unlimited to SetMaxOpenConns and that is the state being closed off.
func parseInt(s string, defaultVal int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

func parseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

func parseAllowlist(env string) []string {
	if env == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(env, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ensurePluginConfigs creates a config row for each registered plugin that does not yet have one.
// defaultConfigFn returns the default config bytes for a plugin ID (or nil to skip).
func ensurePluginConfigs(ctx context.Context, database db.PluginConfigDB, category string, pluginIDs []string, defaultConfigFn func(string) []byte) error {
	for i, id := range pluginIDs {
		_, err := database.GetPluginConfig(ctx, category, id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			cfg := defaultConfigFn(id)
			if cfg == nil {
				continue
			}
			precedence := 10 * (i + 1)
			if _, err := database.InsertPluginConfig(ctx, category, id, false, precedence, cfg, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
