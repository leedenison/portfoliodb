//go:build e2e

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync"
	"syscall"
	"time"

	e2ev1 "github.com/leedenison/portfoliodb/proto/e2e/v1"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// validCassetteName allows alphanumeric characters and hyphens only.
var validCassetteName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// swappableTransport delegates to the current VCR recorder's transport.
// When no cassette is loaded the delegate is nil and RoundTrip returns an
// error, causing any unexpected plugin HTTP call to fail fast.
type swappableTransport struct {
	mu sync.RWMutex
	rt http.RoundTripper
}

func (s *swappableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.RLock()
	rt := s.rt
	s.mu.RUnlock()
	if rt == nil {
		return nil, fmt.Errorf("e2e: no cassette loaded, refusing HTTP call to %s", req.URL)
	}
	return rt.RoundTrip(req)
}

func (s *swappableTransport) swap(rt http.RoundTripper) {
	s.mu.Lock()
	s.rt = rt
	s.mu.Unlock()
}

// Shared mutable state for the E2E recorder and transport.
var (
	e2eMu          sync.Mutex
	e2eRec         *recorder.Recorder
	e2eTransport   = &swappableTransport{}
	e2eCassetteDir string
)

func init() {
	e2eCassetteDir = os.Getenv("E2E_CASSETTE_DIR")
	if e2eCassetteDir == "" {
		e2eCassetteDir = "e2e/cassettes"
	}
	log.Printf("e2e: cassette dir=%s, no cassette loaded at startup", e2eCassetteDir)
}

// e2eService implements the E2eService gRPC interface.
type e2eService struct {
	e2ev1.UnimplementedE2EServiceServer
}

func (s *e2eService) LoadCassette(_ context.Context, req *e2ev1.LoadCassetteRequest) (*e2ev1.LoadCassetteResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "cassette name is required")
	}
	if !validCassetteName.MatchString(name) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid cassette name %q: alphanumeric and hyphens only", name)
	}

	if err := loadCassette(name); err != nil {
		return nil, status.Errorf(codes.Internal, "load cassette: %v", err)
	}
	return &e2ev1.LoadCassetteResponse{}, nil
}

func (s *e2eService) UnloadCassette(context.Context, *e2ev1.UnloadCassetteRequest) (*e2ev1.UnloadCassetteResponse, error) {
	unloadCassette()
	return &e2ev1.UnloadCassetteResponse{}, nil
}

// processStartedAt is when this process began. It survives nothing: a restart
// replaces the process, so a caller comparing this across a restart is told it
// is talking to a new one.
var processStartedAt = time.Now()

// ProcessInfo implements the E2eService liveness and identity probe.
func (s *e2eService) ProcessInfo(context.Context, *e2ev1.ProcessInfoRequest) (*e2ev1.ProcessInfoResponse, error) {
	return &e2ev1.ProcessInfoResponse{StartedAt: timestamppb.New(processStartedAt)}, nil
}

// RestartProcess replaces this process with a fresh copy of itself.
//
// syscall.Exec rather than an exit: the container has no restart policy, and
// giving it one would mean a server that crashed for an unrelated reason came
// back silently in every other suite. Exec keeps the container up and the
// process new, which is the part that matters -- every goroutine, connection
// and queued job is gone, and the run picks up from what was written down.
//
// The reply goes out first and the exec runs just after, so the caller sees the
// call succeed and then the connection drop.
func (s *e2eService) RestartProcess(context.Context, *e2ev1.RestartProcessRequest) (*e2ev1.RestartProcessResponse, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "locate own binary: %v", err)
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		log.Printf("e2e: restarting process")
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			// Exec only returns on failure, and a server that cannot replace
			// itself is no use to the test that asked it to.
			log.Fatalf("e2e: exec %s: %v", exe, err)
		}
	}()
	return &e2ev1.RestartProcessResponse{}, nil
}

func loadCassette(name string) error {
	e2eMu.Lock()
	defer e2eMu.Unlock()

	// Stop existing recorder first (flushes cassette in record mode).
	if e2eRec != nil {
		if err := e2eRec.Stop(); err != nil {
			log.Printf("e2e: stop previous recorder: %v", err)
		}
		e2eRec = nil
		e2eTransport.swap(nil)
	}

	path := e2eCassetteDir + "/" + name
	mode := recorder.ModeReplayOnly
	if vcr.IsRecordingSuite(name) {
		mode = recorder.ModeRecordOnly
	}

	opts := []recorder.Option{
		recorder.WithMode(mode),
		recorder.WithSkipRequestLatency(true),
		recorder.WithHook(vcr.SanitizeAll, recorder.BeforeSaveHook),
		recorder.WithMatcher(vcr.E2EMatcher),
	}

	rec, err := recorder.New(path, opts...)
	if err != nil {
		return fmt.Errorf("create recorder at %s: %w", path, err)
	}

	e2eRec = rec
	e2eTransport.swap(rec.GetDefaultClient().Transport)
	log.Printf("e2e: loaded cassette %q (mode=%v)", name, mode)
	return nil
}

func unloadCassette() {
	e2eMu.Lock()
	defer e2eMu.Unlock()

	if e2eRec != nil {
		if err := e2eRec.Stop(); err != nil {
			log.Printf("e2e: stop recorder: %v", err)
		}
		e2eRec = nil
		log.Printf("e2e: cassette unloaded")
	}
	e2eTransport.swap(nil)
}

func registerE2EService(svc *grpc.Server) {
	e2ev1.RegisterE2EServiceServer(svc, &e2eService{})
	log.Printf("e2e: registered E2eService")
}

func e2eSkipPrefixes() []string {
	return []string{"/portfoliodb.e2e.v1."}
}
