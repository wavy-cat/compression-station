package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v3"
	"github.com/wavy-cat/compression-station/internal/config"
	"github.com/wavy-cat/compression-station/internal/handler/fetcher"
	"github.com/wavy-cat/compression-station/internal/middleware/encoder"
	cache2 "github.com/wavy-cat/compression-station/pkg/cache"
	"github.com/wavy-cat/compression-station/pkg/cache/memory"
	"github.com/wavy-cat/compression-station/pkg/cache/redis"
	"github.com/wavy-cat/compression-station/pkg/storage"
	"github.com/wavy-cat/compression-station/pkg/storage/local"
	"github.com/wavy-cat/compression-station/pkg/storage/s3"
	"go.uber.org/zap"
)

func main() {
	// config
	cfg, err := config.GetConfig("config.yml")
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
	var cache cache2.BytesCache
	switch cfg.Cache.CacheType {
	case config.Memory:
		cache, err = memory.NewLRUCache(cfg.Cache.Memory.Capacity)
	case config.Redis:
		cache, err = redis.NewCache(cfg.Cache.Redis.Address, cfg.Cache.Redis.Password, cfg.Cache.Redis.DB)
	}
	if err != nil {
		logger.Fatal("Failed to create cache", zap.Error(err))
	}
	defer func(cache cache2.BytesCache) {
		err := cache.Close()
		if err != nil {
			logger.Error("Failed to close cache", zap.Error(err))
		}
	}(cache)

	// web server
	app := fiber.New()
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	app.Use(func(c fiber.Ctx) error {
		err := c.Next()
		logger.Debug("HTTP Request",
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.Error(c.Err()),
			zap.String("ua", c.Get("User-Agent")))
		return err
	})

	for _, path := range cfg.Paths {
		formatedPath := fmt.Sprintf("%s/*", path)
		app.Use(formatedPath, encoder.Encoder(store, cache, cfg.FilePattern, logger))
		app.Get(fmt.Sprintf("%s/*", path), fetcher.Fetcher(cfg.Url))
	}

	app.Get("/*", fetcher.Fetcher(cfg.Url))

	// Start server in a goroutine
	logger.Info("Starting server...", zap.String("addr", addr))
	go func() {
		cfg := fiber.ListenConfig{
			DisableStartupMessage: true,
		}
		if err := app.Listen(addr, cfg); err != nil {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")
	if err := app.Shutdown(); err != nil {
		logger.Fatal("Server shutdown failed", zap.Error(err))
	}
	logger.Info("Server stopped gracefully")
}
