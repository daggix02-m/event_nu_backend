package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daggix02-m/event_nu_backend/internal/api"
	"github.com/daggix02-m/event_nu_backend/internal/api/routes"
	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/database"
	"github.com/daggix02-m/event_nu_backend/internal/transport/grpc"
	"github.com/daggix02-m/event_nu_backend/internal/transport/grpc/eventnupb"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	eventgrpcreflection "google.golang.org/grpc/reflection"
)

func main() {
	// .env is a dev convenience only; real environments provide env vars directly.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err.Error())
		os.Exit(1)
	}

	logger := newLogger(cfg.AppEnv)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err.Error())
		os.Exit(1)
	}
	defer pool.Close()

	app := api.NewApp(logger, cfg, pool)
	defer app.RateLimiter.Stop()

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      routes.New(app),
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("server starting", "addr", srv.Addr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err.Error())
			os.Exit(1)
		}
	}()

	// gRPC transport: opt-in via GRPC_ADDR. It shares the same Application
	// services as REST; an unset GRPC_ADDR keeps the deployment REST-only.
	var grpcSrv *grpc.Server
	if cfg.GRPCAddr != "" {
		grpcSrv = grpc.NewServer(
			grpc.ChainUnaryInterceptor(
				transportgrpc.RecoveryInterceptor(logger),
				transportgrpc.LoggingInterceptor(logger),
				// Identity resolves BEFORE the transaction opens so the RLS
				// context carries the caller's id — the mirrored order of
				// REST's RequireAuth/OptionalAuth running inside BeginRequestTx.
				transportgrpc.OptionalAuthInterceptor(app.Auth),
				transportgrpc.TransactionInterceptor(app.DB, transportgrpc.RoleAlways("user"), app.Logger),
			),
		)
		eventnupb.RegisterEventServiceServer(grpcSrv, transportgrpc.NewEventServer(app.Event, app.Media, app.Like, app.Save, app.Logger))
		eventgrpcreflection.Register(grpcSrv)

		lis, err := net.Listen("tcp", cfg.GRPCAddr)
		if err != nil {
			logger.Error("grpc listen failed", "addr", cfg.GRPCAddr, "error", err.Error())
			os.Exit(1)
		}
		logger.Info("grpc server starting", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
		go func() {
			if err := grpcSrv.Serve(lis); err != nil {
				logger.Error("grpc server error", "error", err.Error())
				os.Exit(1)
			}
		}()
	}

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err.Error())
		os.Exit(1)
	}
	if grpcSrv != nil {
		grpcSrv.GracefulStop()
	}
	logger.Info("server stopped")
}

func newLogger(env string) *slog.Logger {
	var handler slog.Handler
	if env == "development" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	return slog.New(handler)
}
