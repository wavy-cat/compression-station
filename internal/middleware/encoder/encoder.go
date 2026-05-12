package encoder

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/wavy-cat/compression-station/pkg/cache"
	"github.com/wavy-cat/compression-station/pkg/delta/dcz"
	"github.com/wavy-cat/compression-station/pkg/storage"
)

// Encoder сжимает контент от fetcher, если запрос удовлетволяет условиям (mime type, regex match)
func Encoder(store storage.Storage, cacheStore cache.BytesCache, filePattern string) func(c fiber.Ctx) error {
	re := regexp.MustCompile(filePattern)

	return func(c fiber.Ctx) error {
		if err := c.Next(); err != nil {
			return err
		}

		// Отсеиваем, если содержимое уже сжато
		if len(c.Response().Header.ContentEncoding()) != 0 {
			return nil
		}

		// Отсеиваем, если Cache-Control не кеширует файл
		if !slices.Contains(strings.Split(c.GetRespHeader("Cache-Control"), ", "), "public") {
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
			return nil
		}

		// Проверяем regex файла
		path := strings.Split(c.Path(), "/")
		filename := path[len(path)-1]
		match := re.MatchString(filename)
		found := re.FindString(filename)
		if !match {
			return nil
		}

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
			return nil
		}

		// Получаем хэш словаря
		availableDictRaw := c.Get("Available-Dictionary")
		if !strings.HasPrefix(availableDictRaw, ":") || !strings.HasSuffix(availableDictRaw, ":") {
			return nil
		}
		availableDict := availableDictRaw[1 : len(availableDictRaw)-1]
		availableDict = strings.ReplaceAll(availableDict, "/", "_")
		availableDict = strings.ReplaceAll(availableDict, "+", "-")

		dictionary, err := getBundleByHash(store, cacheStore, availableDict)
		if err != nil {
			if errors.Is(err, storage.ErrNotExists) {
				return nil
			}
			return err
		}

		// Получаем тело ответа
		body := c.Response().Body()

		encodedBody, err := dcz.CompressDict(dictionary, body)
		if err != nil {
			return err
		}

		// Добавляем заголовки
		c.Set("Content-Encoding", "dcz")

		c.Response().Header.SetContentLength(len(encodedBody))
		return c.Send(encodedBody)
	}
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

func getBundleByHash(storage storage.Storage, cacheStorage cache.BytesCache, hash string) ([]byte, error) {
	cached, err := cacheStorage.Pull(hash)
	if err != nil && !errors.Is(err, cache.ErrNotExists) {
		return nil, err
	}
	if cached != nil {
		return cached, nil
	}

	content, err := storage.Pull(hash)
	if err != nil {
		return nil, err
	}
	go func() {
		_ = cacheStorage.Push(hash, content)
	}()

	return content, err
}

func isMimeAllowed(mimeType string) bool {
	if slices.Contains(allowedMimeTypes, mimeType) {
		return true
	}
	return false
}
