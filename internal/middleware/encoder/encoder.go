package encoder

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dlclark/regexp2/v2"
	"github.com/gofiber/fiber/v3"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/wavy-cat/compression-station/pkg/delta"
	"github.com/wavy-cat/compression-station/pkg/delta/dcz"
	"github.com/wavy-cat/compression-station/pkg/storage"
	"go.uber.org/zap"
)

type cacheCompressorType = lru.Cache[string, delta.Compressor] // Use with lru.NewWithEvict()

// Encoder сжимает контент от fetcher, если запрос удовлетворяет условиям (mime type, regex match)
func Encoder(store storage.Storage, cacheStore *cacheCompressorType, filePattern string, compressionLevel int, logger *zap.Logger) func(c fiber.Ctx) error {
	re := regexp2.MustCompile(filePattern)

	return func(c fiber.Ctx) error {
		if err := c.Next(); err != nil {
			return err
		}

		if reason, ok := isValidRequest(c, re); !ok {
			logger.Debug("skipping encoding", zap.String("reason", reason), zap.String("path", c.Path()))
			return nil
		}

		err := addHeaders(c, re)
		if err != nil {
			logger.Error("error adding headers", zap.Error(err), zap.String("path", c.Path()))
			return nil
		}

		// Проверяем, что клиент поддерживает dcz в заголовке Accept-Encoding
		acceptEncoding := string(c.Request().Header.Peek("Accept-Encoding"))
		if !strings.Contains(acceptEncoding, "dcz") {
			logger.Debug("skipping encoding",
				zap.String("reason", "Accept-Encoding does not contain dcz"),
				zap.String("acceptEncoding", acceptEncoding))
			return nil
		}

		// Получаем хэш словаря
		adValue := c.Get("Available-Dictionary")
		if !isValidADHeaderFormat(adValue) {
			logger.Debug("skipping encoding",
				zap.String("reason", "Available-Dictionary header format is invalid"),
				zap.String("availableDict", adValue))
			return nil
		}
		adHash := extractAvailableDictionary(adValue)

		// Получаем тело ответа
		body := c.Response().Body()

		compressor, err := getCompressor(adHash, compressionLevel, cacheStore, store)
		if err != nil {
			if errors.Is(err, storage.ErrNotExists) {
				logger.Debug("skipping encoding",
					zap.String("reason", "dictionary not found in storage"),
					zap.String("hash", adHash))
				return nil
			}
			logger.Error("error getting compressor", zap.Error(err), zap.String("hash", adHash), zap.String("path", c.Path()))
			return nil
		}

		encodedBody := compressor.Compress(body)

		// Добавляем заголовки
		c.Set("Content-Encoding", "dcz")
		c.Response().Header.SetContentLength(len(encodedBody))

		return c.Send(encodedBody)
	}
}

// extractAvailableDictionary removes surrounding ':' markers from Available-Dictionary and normalizes base64url chars.
func extractAvailableDictionary(headerValue string) string {
	availableDictHash := headerValue[1 : len(headerValue)-1]
	availableDictHash = strings.ReplaceAll(availableDictHash, "/", "_")
	availableDictHash = strings.ReplaceAll(availableDictHash, "+", "-")
	return availableDictHash
}

// isValidADHeaderFormat reports whether headerValue matches the expected Available-Dictionary hash wrapper format.
func isValidADHeaderFormat(headerValue string) bool {
	return strings.HasPrefix(headerValue, ":") &&
		strings.HasSuffix(headerValue, ":")
}

// isValidRequest validates whether the current response is eligible for encoding.
func isValidRequest(c fiber.Ctx, re *regexp2.Regexp) (string, bool) {
	// Отсеиваем, если содержимое уже сжато
	if len(c.Response().Header.ContentEncoding()) != 0 {
		return "content already compressed", false
	}

	// Отсеиваем, если Cache-Control не кеширует файл
	if !slices.Contains(strings.Split(c.GetRespHeader("Cache-Control"), ", "), "public") {
		return "Cache-Control is not public", false
	}

	// Получаем Content-Type
	contentType := string(c.Response().Header.ContentType())

	mimeType := contentType
	if idx := strings.IndexByte(contentType, ';'); idx != -1 {
		mimeType = contentType[:idx]
	}
	mimeType = strings.TrimSpace(mimeType)

	// Проверяем, допускает ли MIME-тип сжатие
	if !isMimeAllowed(mimeType) {
		return "MIME type not allowed", false
	}

	// Проверяем regex файла
	path := strings.Split(c.Path(), "/")
	filename := path[len(path)-1]

	match, err := re.MatchString(filename)
	if err != nil {
		return fmt.Sprintf("error: %v", err), false
	}
	if !match {
		return "filename does not match regex pattern", false
	}

	return "", true
}

// addHeaders adds the `Use-As-Dictionary` and `Vary` headers to the response
func addHeaders(c fiber.Ctx, re *regexp2.Regexp) error {
	match, err := getMatchPattern(c.Path(), re)
	if err != nil {
		return err
	}
	c.Set("Use-As-Dictionary", fmt.Sprintf(`match="%s"`, match))

	c.Set("Vary", "Available-Dictionary, Accept-Encoding")

	return nil
}

// getMatchPattern builds a glob-style match pattern from pathStr using the first regex match in the filename.
func getMatchPattern(pathStr string, re *regexp2.Regexp) (string, error) {
	path := strings.Split(pathStr, "/")
	filename := path[len(path)-1]
	foundMatch, err := re.FindStringMatch(filename)
	if err != nil {
		return "", err
	}
	if foundMatch == nil {
		return "", errors.New("regex match returned no result")
	}
	found := foundMatch.String()

	var matchValue strings.Builder
	for _, p := range path[0 : len(path)-1] {
		matchValue.WriteString(p)
		matchValue.WriteString("/")
	}

	// Разбиваем filename на части по вхождению matched-подстроки,
	// каждую часть экранируем, между ними вставляем "*"
	parts := strings.SplitN(filename, found, 2)
	matchValue.WriteString(escapeGlob(parts[0]))
	matchValue.WriteString("*")
	if len(parts) == 2 {
		matchValue.WriteString(escapeGlob(parts[1]))
	}

	return matchValue.String(), nil
}

func getCompressor(hashDict string, compressionLevel int, cacheStore *cacheCompressorType, storage storage.Storage) (delta.Compressor, error) {
	if compressor, ok := cacheStore.Get(hashDict); ok {
		return compressor, nil
	}

	dictionary, err := storage.Pull(hashDict)
	if err != nil {
		return nil, err
	}

	compressor, err := dcz.NewCompressor(dictionary, compressionLevel)
	if err != nil {
		return nil, err
	}

	return compressor, nil
}

func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isMimeAllowed(mimeType string) bool {
	return slices.Contains(allowedMimeTypes, mimeType)
}
