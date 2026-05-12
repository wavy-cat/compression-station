package redis

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
	"github.com/wavy-cat/compression-station/pkg/cache"
)

type Cache struct {
	client *redis.Client
	ctx    context.Context
}

func NewCache(addr string, password string, db int) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &Cache{
		client: client,
		ctx:    ctx,
	}, nil
}

func (l *Cache) Push(key string, value []byte) error {
	return l.client.Set(l.ctx, key, value, 0).Err()
}

func (l *Cache) Pull(key string) ([]byte, error) {
	value, err := l.client.Get(l.ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, cache.ErrNotExists
	}
	if err != nil {
		return nil, err
	}

	return value, nil
}

func (l *Cache) Close() error {
	return l.client.Close()
}
