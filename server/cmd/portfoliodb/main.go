package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	"github.com/leedenison/portfoliodb/server/auth/allowlist"
	"github.com/leedenison/portfoliodb/server/auth/google"
	"github.com/leedenison/portfoliodb/server/auth/session"
	"github.com/leedenison/portfoliodb/server/db/postgres"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/api"
	authservice "github.com/leedenison/portfoliodb/server/service/auth"
	"github.com/leedenison/portfoliodb/server/service/ingestion"
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

	pluginRegistry := identifier.NewRegistry()
	queue := make(chan *ingestion.JobRequest, 256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ingestion.RunWorker(ctx, database, queue, pluginRegistry)
	svc := grpc.NewServer(
		grpc.ChainUnaryInterceptor(auth.UnaryInterceptor(interceptorConfig)),
		grpc.ChainStreamInterceptor(auth.StreamInterceptor(interceptorConfig)),
	)
	authv1.RegisterAuthServiceServer(svc, authServer)
	apiv1.RegisterApiServiceServer(svc, api.NewServer(database))
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
