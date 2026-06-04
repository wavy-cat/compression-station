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

func parseCliArgs() string {
	configPath := flag.String("c", "config.yml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("compression-station %s %s (%s %s/%s)\n", //nolint:forbidigo // intentional version output to stdout
			version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	return *configPath
}

func getLogger(cfg config.Logger) *zap.Logger {
	level, err := zap.ParseAtomicLevel(cfg.Level)
	if err != nil {
		panic(fmt.Sprintf("invalid log level: %v", err))
	}

	var zapConfig zap.Config
	switch cfg.Preset {
	case config.ProdPreset:
		zapConfig = zap.NewProductionConfig()
	case config.DevPreset:
		zapConfig = zap.NewDevelopmentConfig()
	default:
		zapConfig = zap.NewDevelopmentConfig()
	}
	zapConfig.Level = level

	logger, err := zapConfig.Build()
	if err != nil {
		panic(fmt.Sprintf("failed to build logger: %v", err))
	}
	return logger
}

func getStorage(cfg config.Storage) (storage.Storage, error) {
	var store storage.Storage
	var err error

	switch cfg.StorageType {
	case config.Local:
		store, err = local.NewStorage(cfg.Local.DirectoryPath)
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
		return nil, err
	}

	return store, nil
}

func setupRouting(
	cfg config.Config,
	logger *zap.Logger,
	store storage.Storage,
	cache *encoder.CompressorCache,
) *chi.Mux {
	r := chi.NewRouter()
	r.Use(logMiddleware.Logger(logger))

	if cfg.Heartbeat.Enable {
		r.Use(chiMiddleware.Heartbeat(cfg.Heartbeat.Path))
	}

	fetcherFunc, err := fetcher.Fetcher(&cfg.Url.URL)
	if err != nil {
		logger.Fatal("failed to create feather", zap.Error(err))
	}

	for _, path := range cfg.Paths {
		r.Route(path, func(r chi.Router) {
			r.Use(encoder.Encoder(
				store,
				cache,
				cfg.FilePattern,
				cfg.ZstdCompressionLevel,
				cfg.BrotliCompressionLevel,
				cfg.PreferEncoder,
				logger,
			))
			r.Get("/*", fetcherFunc)
		})
	}

	r.HandleFunc("/*", fetcherFunc)

	return r
}

func main() {
	// cli args
	configPath := parseCliArgs()

	// config
	cfg, err := config.GetConfig(configPath)
	if err != nil {
		panic(err)
	}

	// logger
	logger := getLogger(cfg.Logger)
	defer func(logger *zap.Logger) {
		_ = logger.Sync()
	}(logger)

	// storage
	store, err := getStorage(cfg.Storage)
	if err != nil {
		logger.Fatal("failed to initialize storage", zap.Error(err))
	}
	defer func(store storage.Storage) {
		if err = store.Close(); err != nil {
			logger.Error("Failed to close storage", zap.Error(err))
		}
	}(store)

	// cache
	compressorCache, err := lru.NewWithEvict(cfg.Size, func(_ string, value delta.Compressor) {
		value.Release()
	})
	if err != nil {
		logger.Fatal("Failed to create cache", zap.Error(err))
	}

	// routing
	r := setupRouting(cfg, logger, store, compressorCache)

	// web server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		WriteTimeout: cfg.Timeouts.Write,
		ReadTimeout:  cfg.Timeouts.Read,
		IdleTimeout:  cfg.Timeouts.Idle,
		Handler:      r,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// start server
	logger.Info("Starting server...", zap.String("addr", addr))
	go func() {
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	// stop server
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown)
	defer cancel()

	logger.Info("Shutting down server...")
	if err = srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server shutdown failed", zap.Error(err))
	}
	logger.Info("Server stopped gracefully")
}
