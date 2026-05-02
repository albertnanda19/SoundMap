// Package interceptors provides the gRPC interceptor chain for all SoundMap services.
// The chain order is critical for proper request handling, observability, and error recovery.
package interceptors

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/soundmap/soundmap/internal/auth"
	"github.com/soundmap/soundmap/internal/ratelimit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ChainUnaryInterceptor returns the correct order of unary interceptors.
// Order (outer to inner): Recovery -> Auth -> Rate Limit -> Logging -> Handler
// The order is REVERSED when chaining because each interceptor wraps the next.
func ChainUnaryInterceptor(jwtSecret string, logger *slog.Logger) grpc.ServerOption {
	return grpc.ChainUnaryInterceptor(
		// 1. RECOVERY (outermost) - catches panics from all inner interceptors and handlers
		// Must be first so it can recover from any panic in auth, rate limit, logging, etc.
		recoveryUnaryInterceptor(logger),

		// 2. AUTH - validates JWT before any processing
		// Runs after recovery (so panics in auth are caught) but before rate limiting
		// so rate limits apply to authenticated users only (not anonymous requests)
		authUnaryInterceptor(jwtSecret, logger),

		// 3. RATE LIMIT - applies per-user rate limiting
		// Runs after auth so we have user identity for rate limit keys
		ratelimitUnaryInterceptor(logger),

		// 4. LOGGING - captures request/response with user context and final status
		// Runs after rate limit so we log actual processed requests (not rejected)
		loggingUnaryInterceptor(logger),
	)
}

// ChainStreamInterceptor returns the correct order of stream interceptors.
// Same ordering logic as unary: Recovery -> Auth -> Rate Limit -> Logging -> OTel -> Handler
func ChainStreamInterceptor(jwtSecret string, logger *slog.Logger) grpc.ServerOption {
	return grpc.ChainStreamInterceptor(
		// 1. RECOVERY (outermost)
		recoveryStreamInterceptor(logger),

		// 2. AUTH - validates JWT for the stream
		authStreamInterceptor(jwtSecret, logger),

		// 3. RATE LIMIT - per-user stream rate limiting
		ratelimitStreamInterceptor(logger),

		// 4. LOGGING - stream open/close events and errors
		loggingStreamInterceptor(logger),
	)
}

// recoveryUnaryInterceptor recovers from panics and converts them to gRPC errors.
func recoveryUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered in gRPC handler",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// recoveryStreamInterceptor recovers from panics in streaming RPCs.
func recoveryStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered in gRPC stream handler",
					slog.String("method", info.FullMethod),
					slog.Any("panic", r),
					slog.String("stack", string(debug.Stack())),
				)
			}
		}()
		return handler(srv, stream)
	}
}

// authUnaryInterceptor validates JWT tokens from metadata.
func authUnaryInterceptor(jwtSecret string, logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Skip auth for health endpoints
		if info.FullMethod == "/grpc.health.v1.Health/Check" ||
			info.FullMethod == "/grpc.health.v1.Health/Watch" {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing metadata")
		}

		token := auth.ExtractTokenFromMetadata(md)
		if token == "" {
			return nil, status.Errorf(codes.Unauthenticated, "missing authorization token")
		}

		claims, err := auth.ValidateToken(token, jwtSecret)
		if err != nil {
			logger.Warn("invalid token",
				slog.String("method", info.FullMethod),
				slog.String("error", err.Error()),
			)
			return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
		}

		// Add claims to context for downstream use
		ctx = auth.WithClaims(ctx, claims)
		return handler(ctx, req)
	}
}

// authStreamInterceptor validates JWT tokens for streaming RPCs.
func authStreamInterceptor(jwtSecret string, logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Skip auth for health endpoints
		if info.FullMethod == "/grpc.health.v1.Health/Check" ||
			info.FullMethod == "/grpc.health.v1.Health/Watch" {
			return handler(srv, stream)
		}

		ctx := stream.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Errorf(codes.Unauthenticated, "missing metadata")
		}

		token := auth.ExtractTokenFromMetadata(md)
		if token == "" {
			return status.Errorf(codes.Unauthenticated, "missing authorization token")
		}

		claims, err := auth.ValidateToken(token, jwtSecret)
		if err != nil {
			logger.Warn("invalid token",
				slog.String("method", info.FullMethod),
				slog.String("error", err.Error()),
			)
			return status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
		}

		// Wrap stream with context containing claims
		wrapped := &wrappedStream{ServerStream: stream, ctx: auth.WithClaims(ctx, claims)}
		return handler(srv, wrapped)
	}
}

// ratelimitUnaryInterceptor applies per-method rate limiting.
func ratelimitUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Get per-method limiter
		limiter := ratelimit.GetLimiter(info.FullMethod)
		if !limiter.Allow() {
			logger.Warn("rate limit exceeded",
				slog.String("method", info.FullMethod),
			)
			return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// ratelimitStreamInterceptor applies rate limiting for streaming RPCs.
func ratelimitStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		limiter := ratelimit.GetLimiter(info.FullMethod)
		if !limiter.Allow() {
			logger.Warn("stream rate limit exceeded",
				slog.String("method", info.FullMethod),
			)
			return status.Errorf(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, stream)
	}
}

// loggingUnaryInterceptor logs request processing time and status.
func loggingUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		logger.Debug("gRPC request started",
			slog.String("method", info.FullMethod),
		)

		resp, err := handler(ctx, req)

		statusCode := codes.OK
		if err != nil {
			if s, ok := status.FromError(err); ok {
				statusCode = s.Code()
			} else {
				statusCode = codes.Unknown
			}
		}

		logger.Info("gRPC request completed",
			slog.String("method", info.FullMethod),
			slog.Duration("duration", time.Since(start)),
			slog.String("status", statusCode.String()),
		)
		return resp, err
	}
}

// loggingStreamInterceptor logs stream lifecycle events.
func loggingStreamInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		logger.Info("gRPC stream started",
			slog.String("method", info.FullMethod),
		)

		start := time.Now()
		err := handler(srv, stream)

		statusCode := codes.OK
		if err != nil {
			if s, ok := status.FromError(err); ok {
				statusCode = s.Code()
			} else {
				statusCode = codes.Unknown
			}
		}

		logger.Info("gRPC stream completed",
			slog.String("method", info.FullMethod),
			slog.Duration("duration", time.Since(start)),
			slog.String("status", statusCode.String()),
		)
		return err
	}
}

// wrappedStream wraps a ServerStream with a custom context.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}
