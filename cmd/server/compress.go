package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/wavy-cat/compression-station/pkg/content"
)

const outputFileMode = 0o600

func runCompression(srcDir, destDir string) error {
	srcAbs, destAbs, err := resolveCompressionPaths(srcDir, destDir)
	if err != nil {
		return err
	}
	if err = validateCompressionPaths(srcAbs, destAbs); err != nil {
		return err
	}
	if err = os.MkdirAll(destAbs, 0o750); err != nil {
		return err
	}

	srcRoot, destRoot, err := openCompressionRoots(srcAbs, destAbs)
	if err != nil {
		return err
	}
	defer func() { _ = srcRoot.Close() }()
	defer func() { _ = destRoot.Close() }()

	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return err
	}
	defer encoder.Close()

	return compressRootFiles(srcRoot, destRoot, encoder)
}

func resolveCompressionPaths(srcDir, destDir string) (string, string, error) {
	srcAbs, err := filepath.Abs(srcDir)
	if err != nil {
		return "", "", err
	}

	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return "", "", err
	}

	return srcAbs, destAbs, nil
}

func validateCompressionPaths(srcAbs, destAbs string) error {
	if srcAbs == destAbs || strings.HasPrefix(destAbs, srcAbs+string(os.PathSeparator)) {
		return errors.New("dest_dir must not be inside src_dir")
	}
	return nil
}

func openCompressionRoots(srcAbs, destAbs string) (*os.Root, *os.Root, error) {
	srcRoot, err := os.OpenRoot(srcAbs)
	if err != nil {
		return nil, nil, err
	}

	destRoot, err := os.OpenRoot(destAbs)
	if err != nil {
		_ = srcRoot.Close()
		return nil, nil, err
	}

	return srcRoot, destRoot, nil
}

func compressRootFiles(srcRoot, destRoot *os.Root, encoder *zstd.Encoder) error {
	return fs.WalkDir(srcRoot.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipCompressionEntry(d) {
			return nil
		}
		return compressRootFile(srcRoot, destRoot, encoder, path)
	})
}

func shouldSkipCompressionEntry(entry fs.DirEntry) bool {
	return entry.IsDir() || !entry.Type().IsRegular()
}

func compressRootFile(srcRoot, destRoot *os.Root, encoder *zstd.Encoder, path string) error {
	data, err := srcRoot.ReadFile(path)
	if err != nil {
		return err
	}

	mimeType := content.DetectMimeType(path, data)
	if !content.IsAllowedMimeType(mimeType) {
		return nil
	}

	compressed := encoder.EncodeAll(data, nil)
	if len(compressed) == 0 {
		return fmt.Errorf("failed to compress %s", path)
	}

	return destRoot.WriteFile(content.HashName(data), compressed, outputFileMode)
}
