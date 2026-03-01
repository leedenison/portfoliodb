package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/postgres"
	"github.com/leedenison/portfoliodb/server/identifier"
	localidentifier "github.com/leedenison/portfoliodb/server/plugins/local/identifier"
	"github.com/leedenison/portfoliodb/server/service/api"
	"github.com/leedenison/portfoliodb/server/service/ingestion"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Auth policy: method/prefix constants for the interceptor.
var (
	authSkipPrefixes     = []string{"/grpc.reflection."}
	authOptionalMethods  = []string{"/portfoliodb.api.v1.ApiService/CreateUser"}
	authInterceptorConfig = auth.InterceptorConfig{
		SkipAuthPrefixes:    authSkipPrefixes,
		OptionalAuthMethods: authOptionalMethods,
	}
)

func main() {
	grpcAddr := flag.String("grpc-addr", envOrDefault("PORTFOLIODB_GRPC_ADDR", ":50051"), "gRPC listen address")
	dbURL := flag.String("db-url", os.Getenv("PORTFOLIODB_DB_URL"), "PostgreSQL connection URL")
	flag.Parse()
	if *dbURL == "" {
		log.Fatal("PORTFOLIODB_DB_URL or -db-url required")
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
	pluginRegistry := identifier.NewRegistry()
	localidentifier.RegisterLocal(pluginRegistry, conn)
	queue := make(chan *ingestion.JobRequest, 256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ingestion.RunWorker(ctx, database, queue, pluginRegistry)
	svc := grpc.NewServer(
		grpc.ChainUnaryInterceptor(auth.UnaryInterceptor(database, authInterceptorConfig)),
		grpc.ChainStreamInterceptor(auth.StreamInterceptor(database, authInterceptorConfig)),
	)
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
