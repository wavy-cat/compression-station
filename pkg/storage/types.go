package storage

import "errors"

var ErrNotExists = errors.New("not exists")

type Storage interface {
	Push(key string, data []byte) error
	Pull(key string) ([]byte, error)
	Close() error
}
