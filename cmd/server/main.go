package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
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

type cliArgs struct {
	compressDest string
	compressMode bool
	compressSrc  string
	configPath   string
	showVersion  bool
}

func parseCliArgs() cliArgs {
	args := os.Args[1:]
	if len(args) == 3 && args[0] == "-compress" {
		return cliArgs{
			compressDest: args[2],
			compressMode: true,
			compressSrc:  args[1],
		}
	}

	configPath := flag.String("c", "config.yml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	return cliArgs{
		configPath:  *configPath,
		showVersion: *showVersion,
	}
}

func printVersion() {
	fmt.Printf("compression-station %s %s (%s %s/%s)\n", //nolint:forbidigo // intentional version output to stdout
		version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func newBootstrapLogger() *zap.Logger {
	logger, err := zap.NewDevelopment()
	if err != nil {
		return zap.NewNop()
	}
	return logger
}

func getLogger(cfg config.Logger) (*zap.Logger, error) {
	level, err := zap.ParseAtomicLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
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
		return nil, fmt.Errorf("failed to build logger: %w", err)
	}
	return logger, nil
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

func run() int {
	bootstrapLogger := newBootstrapLogger()
	defer func() { _ = bootstrapLogger.Sync() }()

	args := parseCliArgs()
	if args.showVersion {
		printVersion()
		return 0
	}
	if args.compressMode {
		if err := runCompression(args.compressSrc, args.compressDest); err != nil {
			bootstrapLogger.Error("compression failed", zap.Error(err))
			return 1
		}
		return 0
	}

	cfg, err := config.GetConfig(args.configPath)
	if err != nil {
		bootstrapLogger.Error("failed to read config", zap.Error(err))
		return 1
	}

	logger, err := getLogger(cfg.Logger)
	if err != nil {
		bootstrapLogger.Error("failed to initialize logger", zap.Error(err))
		return 1
	}
	defer func() {
		_ = logger.Sync()
	}()

	store, err := getStorage(cfg.Storage)
	if err != nil {
		logger.Error("failed to initialize storage", zap.Error(err))
		return 1
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			logger.Error("Failed to close storage", zap.Error(closeErr))
		}
	}()

	compressorCache, err := lru.NewWithEvict(cfg.Size, func(_ string, value delta.Compressor) {
		value.Release()
	})
	if err != nil {
		logger.Error("Failed to create cache", zap.Error(err))
		return 1
	}

	r := setupRouting(cfg, logger, store, compressorCache)

	addr := net.JoinHostPort(cfg.Server.Host, strconv.FormatUint(uint64(cfg.Server.Port), 10))
	srv := &http.Server{
		Addr:         addr,
		WriteTimeout: cfg.Timeouts.Write,
		ReadTimeout:  cfg.Timeouts.Read,
		IdleTimeout:  cfg.Timeouts.Idle,
		Handler:      r,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("Starting server...", zap.String("addr", addr))
	go func() {
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown)
	defer cancel()

	logger.Info("Shutting down server...")
	if err = srv.Shutdown(ctx); err != nil {
		logger.Fatal("Server shutdown failed", zap.Error(err))
	}
	logger.Info("Server stopped gracefully")
	return 0
}

func main() {
	os.Exit(run())
}
