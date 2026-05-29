package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/wavy-cat/compression-station/internal/config"
	"github.com/wavy-cat/compression-station/internal/handler/fetcher"
	"github.com/wavy-cat/compression-station/internal/middleware/encoder"
	logMiddleware "github.com/wavy-cat/compression-station/internal/middleware/logger"
	"github.com/wavy-cat/compression-station/pkg/delta"
	"github.com/wavy-cat/compression-station/pkg/storage"
	"github.com/wavy-cat/compression-station/pkg/storage/local"
	"github.com/wavy-cat/compression-station/pkg/storage/s3"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	// cli args
	configPath := flag.String("c", "config.yml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("compression-station %s %s (%s %s/%s)\n",
			version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	// config
	cfg, err := config.GetConfig(*configPath)
	if err != nil {
		panic(err)
	}

	// logger
	level, err := zap.ParseAtomicLevel(cfg.Logger.Level)
	if err != nil {
		panic(fmt.Sprintf("invalid log level: %v", err))
	}

	var zapConfig zap.Config
	switch cfg.Logger.Preset {
	case config.ProdPreset:
		zapConfig = zap.NewProductionConfig()
	case config.DevPreset:
		zapConfig = zap.NewDevelopmentConfig()
	}
	zapConfig.Level = level

	logger, err := zapConfig.Build()
	if err != nil {
		panic(fmt.Sprintf("failed to build logger: %v", err))
	}
	//goland:noinspection ALL
	defer logger.Sync()

	// storage
	var store storage.Storage
	switch cfg.Storage.StorageType {
	case config.Local:
		store, err = local.NewStorage(cfg.Storage.Local.DirectoryPath)
	case config.S3:
		store, err = s3.NewStorage(context.Background(), s3.Config{
			Bucket:      cfg.S3.Bucket,
			Region:      cfg.S3.Region,
			Endpoint:    cfg.S3.Endpoint,
			Prefix:      cfg.S3.Prefix,
			AccessToken: cfg.S3.AccessToken,
			SecretToken: cfg.S3.SecretToken,
		})
	}
	if err != nil {
		logger.Fatal("Failed to create storage", zap.Error(err))
	}
	defer func(store storage.Storage) {
		err := store.Close()
		if err != nil {
			logger.Error("Failed to close storage", zap.Error(err))
		}
	}(store)

	// cache
	compressorCache, err := lru.NewWithEvict(cfg.Size, func(key string, value delta.Compressor) {
		value.Release()
	})
	if err != nil {
		logger.Fatal("Failed to create cache", zap.Error(err))
	}

	// routing
	r := chi.NewRouter()
	r.Use(logMiddleware.Logger(logger))

	if cfg.Heartbeat.Enable {
		r.Use(chiMiddleware.Heartbeat(cfg.Heartbeat.Path))
	}

	for _, path := range cfg.Paths {
		r.Route(path, func(r chi.Router) {
			r.Use(encoder.Encoder(store, compressorCache, cfg.FilePattern, cfg.CompressionLevel, logger))
			r.Get("/*", fetcher.Fetcher(cfg.Url))
		})
	}

	r.HandleFunc("/*", fetcher.Fetcher(cfg.Url))

	// web server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	logger.Info("Starting server...", zap.String("addr", addr))
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout*time.Millisecond)
	defer cancel()

	logger.Info("Shutting down server...")
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server shutdown failed", zap.Error(err))
	}
	logger.Info("Server stopped gracefully")
}
