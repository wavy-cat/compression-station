package local

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/wavy-cat/compression-station/pkg/storage"
)

type Storage struct {
	dir string

	mu        sync.RWMutex
	closingWg sync.WaitGroup
	closed    bool
}

// NewStorage creates a new file system storage with the specified directory for storage.
func NewStorage(dir string) (*Storage, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0o750)
		if err != nil {
			return nil, err
		}
	}

	return &Storage{dir: dir}, nil
}

func (fsc *Storage) Push(key string, value []byte) error {
	if err := fsc.beginOperation(); err != nil {
		return err
	}
	defer fsc.endOperation()

	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return err
	}
	defer encoder.Close()

	compressed := encoder.EncodeAll(value, nil)

	filePath := filepath.Join(fsc.dir, key)
	return os.WriteFile(filePath, compressed, 0o600)
}

func (fsc *Storage) Pull(key string) ([]byte, error) {
	if err := fsc.beginOperation(); err != nil {
		return nil, err
	}
	defer fsc.endOperation()

	filePath := filepath.Join(fsc.dir, key)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotExists
		}
		return nil, err
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	decompressed, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, err
	}

	return decompressed, nil
}

func (fsc *Storage) Close() error {
	fsc.mu.Lock()
	if fsc.closed {
		fsc.mu.Unlock()
		return errors.New("storage is already closed")
	}
	fsc.closed = true
	fsc.mu.Unlock()

	fsc.closingWg.Wait()
	return nil
}

func (fsc *Storage) beginOperation() error {
	fsc.mu.RLock()
	defer fsc.mu.RUnlock()

	if fsc.closed {
		return errors.New("storage is closed")
	}

	fsc.closingWg.Add(1)
	return nil
}

func (fsc *Storage) endOperation() {
	fsc.closingWg.Done()
}
