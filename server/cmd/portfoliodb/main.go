package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	"github.com/leedenison/portfoliodb/server/auth/allowlist"
	"github.com/leedenison/portfoliodb/server/auth/google"
	"github.com/leedenison/portfoliodb/server/auth/session"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/migrate"
	"github.com/leedenison/portfoliodb/server/db/postgres"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/logger"
	"github.com/leedenison/portfoliodb/server/migrations"
	cashdesc "github.com/leedenison/portfoliodb/server/plugins/cash/description"
	cashid "github.com/leedenison/portfoliodb/server/plugins/cash/identifier"
	eodhdplugin "github.com/leedenison/portfoliodb/server/plugins/eodhd/identifier"
	massiveplugin "github.com/leedenison/portfoliodb/server/plugins/massive/identifier"
	massiveprice "github.com/leedenison/portfoliodb/server/plugins/massive/price"
	openfigiplugin "github.com/leedenison/portfoliodb/server/plugins/openfigi/identifier"
	openaidesc "github.com/leedenison/portfoliodb/server/plugins/openai/description"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/service/api"
	authservice "github.com/leedenison/portfoliodb/server/service/auth"
	"github.com/leedenison/portfoliodb/server/service/ingestion"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/redis/go-redis/v9"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	grpcAddr := flag.String("grpc-addr", envOrDefault("PORTFOLIODB_GRPC_ADDR", ":50051"), "gRPC listen address")
	dbURL := flag.String("db-url", os.Getenv("PORTFOLIODB_DB_URL"), "PostgreSQL connection URL")
	redisURL := flag.String("redis-url", envOrDefault("PORTFOLIODB_REDIS_URL", os.Getenv("REDIS_URL")), "Redis connection URL for sessions")
	flag.Parse()
	if *dbURL == "" {
		log.Fatal("PORTFOLIODB_DB_URL or -db-url required")
	}
	if *redisURL == "" {
		log.Fatal("PORTFOLIODB_REDIS_URL or REDIS_URL required")
	}
	conn, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, conn, migrations.Files); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	database := postgres.New(conn)

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

	counterPrefix := "portfoliodb:counters:"
	counter := telemetry.NewRedisCounter(rdb, counterPrefix)
	inner := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	})
	logLevelEnv := envOrDefault("LOG_LEVEL", "debug")
	h := logger.NewHandler(inner, logLevelEnv)
	slog.SetDefault(slog.New(h))
	serverLogger := slog.Default()
	serverLogger.Info("LOG_LEVEL configured", "levels", logger.Summary(logLevelEnv))

	// Google ID token verifier
	googleClientID := os.Getenv("GOOGLE_OAUTH_CLIENT_ID")
	if googleClientID == "" {
		log.Fatal("GOOGLE_OAUTH_CLIENT_ID required")
	}
	jwksTTL := parseDuration(os.Getenv("GOOGLE_JWKS_CACHE_TTL"), google.DefaultJWKSCacheTTL)
	clockSkew := parseDuration(os.Getenv("GOOGLE_TOKEN_CLOCK_SKEW"), google.DefaultClockSkew)
	verifier := google.NewVerifier(googleClientID,
		google.WithJWKSCacheTTL(jwksTTL),
		google.WithClockSkew(clockSkew),
	)

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
	authServer := authservice.NewServer(
		verifier,
		sessionStore,
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
		os.Getenv("ADMIN_AUTH_SUB"),
	)

	interceptorConfig := auth.InterceptorConfig{
		SkipAuthPrefixes:       []string{"/grpc.reflection."},
		NoSessionMethods:       []string{"/portfoliodb.auth.v1.AuthService/Auth"},
		OptionalSessionMethods: []string{"/portfoliodb.auth.v1.AuthService/Logout"},
		SessionStore:           sessionStore,
		SessionCookieName:       cookieName,
		ExtendTTL:              extendTTL,
	}

	pluginHTTPClient := &http.Client{Timeout: 30 * time.Second}
	pluginRegistry := identifier.NewRegistry()
	pluginRegistry.Register(openfigiplugin.PluginID, openfigiplugin.NewPlugin(counter, logger.WithCategory(serverLogger, "server/plugins/openfigi"), pluginHTTPClient))
	pluginRegistry.Register(massiveplugin.PluginID, massiveplugin.NewPlugin(counter, logger.WithCategory(serverLogger, "server/plugins/massive"), pluginHTTPClient))
	pluginRegistry.Register(eodhdplugin.PluginID, eodhdplugin.NewPlugin(counter, logger.WithCategory(serverLogger, "server/plugins/eodhd"), pluginHTTPClient))
	pluginRegistry.Register(cashid.PluginID, cashid.NewPlugin(database))
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryIdentifier, pluginRegistry.ListIDs(), func(id string) []byte {
		if p := pluginRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure identifier plugin configs: %v", err)
	}
	descRegistry := description.NewRegistry()
	descRegistry.Register(openaidesc.PluginID, openaidesc.NewPlugin(counter, logger.WithCategory(serverLogger, "server/plugins/openai"), &http.Client{Timeout: 20 * time.Second}))
	descRegistry.Register(cashdesc.PluginID, cashdesc.NewPlugin())
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryDescription, descRegistry.ListIDs(), func(id string) []byte {
		if p := descRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure description plugin configs: %v", err)
	}
	priceRegistry := pricefetcher.NewRegistry()
	priceRegistry.Register(massiveprice.PluginID, massiveprice.NewPlugin(counter, logger.WithCategory(serverLogger, "server/plugins/massive/price"), pluginHTTPClient))
	if err := ensurePluginConfigs(context.Background(), database, db.PluginCategoryPrice, priceRegistry.ListIDs(), func(id string) []byte {
		if p := priceRegistry.Get(id); p != nil {
			return p.DefaultConfig()
		}
		return nil
	}); err != nil {
		log.Fatalf("ensure price plugin configs: %v", err)
	}
	priceTrigger := make(chan struct{}, 1)
	queue := make(chan *ingestion.JobRequest, 256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ingestionLogger := logger.WithCategory(serverLogger, "server/service/ingestion")
	go ingestion.RunWorker(ctx, database, queue, pluginRegistry, descRegistry, counter, ingestionLogger, priceTrigger)
	go pricefetcher.RunWorker(ctx, database, priceRegistry, counter, logger.WithCategory(serverLogger, "server/pricefetcher"), priceTrigger)
	svc := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			logger.UnaryErrorInterceptor(serverLogger),
			auth.UnaryInterceptor(interceptorConfig),
		),
		grpc.ChainStreamInterceptor(
			logger.StreamErrorInterceptor(serverLogger),
			auth.StreamInterceptor(interceptorConfig),
		),
	)
	authv1.RegisterAuthServiceServer(svc, authServer)
	apiv1.RegisterApiServiceServer(svc, api.NewServer(api.ServerConfig{
		DB:             database,
		Redis:          rdb,
		CounterPrefix:  counterPrefix,
		PluginRegistry: pluginRegistry,
		DescRegistry:   descRegistry,
		PriceRegistry:  priceRegistry,
		PriceTrigger:   priceTrigger,
	}))
	ingestionv1.RegisterIngestionServiceServer(svc, ingestion.NewServer(database, queue))
	reflection.Register(svc)
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
	log.Printf("listening on %s", *grpcAddr)
	if err := svc.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
