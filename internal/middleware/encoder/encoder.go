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

		// Отсеиваем, если содержимое уже сжато
		if len(c.Response().Header.ContentEncoding()) != 0 {
			logger.Debug("Skipping encoding: content already compressed")
			return nil
		}

		// Отсеиваем, если Cache-Control не кеширует файл
		if !slices.Contains(strings.Split(c.GetRespHeader("Cache-Control"), ", "), "public") {
			logger.Debug("Skipping encoding: Cache-Control is not public")
			return nil
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
			logger.Debug("Skipping encoding: MIME type not allowed", zap.String("mimeType", mimeType))
			return nil
		}

		// Проверяем regex файла
		path := strings.Split(c.Path(), "/")
		filename := path[len(path)-1]

		match, err := re.MatchString(filename)
		if err != nil {
			return err
		}
		if !match {
			logger.Debug("Skipping encoding: filename does not match regex pattern", zap.String("filename", filename))
			return nil
		}

		foundMatch, err := re.FindStringMatch(filename)
		if err != nil {
			return err
		}
		if foundMatch == nil {
			logger.Debug("Skipping encoding: regex match returned no result", zap.String("filename", filename))
			return nil
		}
		found := foundMatch.String()

		// Добавляем заголовок, что файл можно использовать как словарь
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
		c.Set("Use-As-Dictionary", fmt.Sprintf(`match="%s"`, matchValue.String()))

		// Добавляем Vary
		c.Set("Vary", "Available-Dictionary, Accept-Encoding")

		// Проверяем Accept-Encoding клиента
		acceptEncoding := string(c.Request().Header.Peek("Accept-Encoding"))
		if !strings.Contains(acceptEncoding, "dcz") {
			logger.Debug("Skipping encoding: Accept-Encoding does not contain dcz", zap.String("acceptEncoding", acceptEncoding))
			return nil
		}

		// Получаем хэш словаря
		availableDictRaw := c.Get("Available-Dictionary")
		if !strings.HasPrefix(availableDictRaw, ":") || !strings.HasSuffix(availableDictRaw, ":") {
			logger.Debug("Skipping encoding: Available-Dictionary header format is invalid", zap.String("availableDict", availableDictRaw))
			return nil
		}
		hashAvailableDict := availableDictRaw[1 : len(availableDictRaw)-1]
		hashAvailableDict = strings.ReplaceAll(hashAvailableDict, "/", "_")
		hashAvailableDict = strings.ReplaceAll(hashAvailableDict, "+", "-")

		// Получаем тело ответа
		body := c.Response().Body()

		compressor, err := getCompressor(hashAvailableDict, compressionLevel, cacheStore, store)
		if err != nil {
			if errors.Is(err, storage.ErrNotExists) {
				logger.Debug("Skipping encoding: dictionary not found in storage", zap.String("hash", hashAvailableDict))
				return nil
			}
			return err
		}

		encodedBody := compressor.Compress(body)

		// Добавляем заголовки
		c.Set("Content-Encoding", "dcz")

		c.Response().Header.SetContentLength(len(encodedBody))
		return c.Send(encodedBody)
	}
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
	if slices.Contains(allowedMimeTypes, mimeType) {
		return true
	}
	return false
}
