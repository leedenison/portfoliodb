package logger

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// is5xx returns true for gRPC codes that map to HTTP 5xx status codes.
func is5xx(c codes.Code) bool {
	switch c {
	case codes.Unknown, codes.Internal, codes.Unavailable, codes.DataLoss:
		return true
	default:
		return false
	}
}

// UnaryErrorInterceptor returns a gRPC unary interceptor that logs 5xx errors.
func UnaryErrorInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			if c := status.Code(err); is5xx(c) {
				log.ErrorContext(ctx, "rpc error",
					"method", info.FullMethod,
					"code", c.String(),
					"error", err.Error(),
				)
			}
		}
		return resp, err
	}
}

// StreamErrorInterceptor returns a gRPC stream interceptor that logs 5xx errors.
func StreamErrorInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)
		if err != nil {
			if c := status.Code(err); is5xx(c) {
				log.ErrorContext(ss.Context(), "rpc error",
					"method", info.FullMethod,
					"code", c.String(),
					"error", err.Error(),
				)
			}
		}
		return err
	}
}
