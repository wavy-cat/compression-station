package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/wavy-cat/compression-station/internal/config"
	"github.com/wavy-cat/compression-station/pkg/delta"
	"github.com/wavy-cat/compression-station/pkg/delta/dcb"
	"github.com/wavy-cat/compression-station/pkg/delta/dcz"
	"github.com/wavy-cat/compression-station/pkg/storage"
	"go.uber.org/zap"
)

// responseRecorder перехватывает ответ от следующего обработчика,
// чтобы мидлварь могла обработать тело и заголовки перед отправкой клиенту.
type responseRecorder struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	path       string
}

func newResponseRecorder(path string) *responseRecorder {
	return &responseRecorder{
		header:     make(http.Header),
		statusCode: http.StatusOK,
		path:       path,
	}
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

// flush копирует накопленные заголовки, статус и тело в реальный ResponseWriter.
func (r *responseRecorder) flush(w http.ResponseWriter) error {
	for key, values := range r.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(r.statusCode)
	// #nosec G705 -- middleware proxies upstream response body as-is, no XSS sink here
	_, err := w.Write(r.body.Bytes())
	return err
}

// Encoder сжимает контент от fetcher, если запрос удовлетворяет условиям (mime type, regex match).
func Encoder(
	store storage.Storage,
	cacheStore *CompressorCache,
	filePattern string,
	zstdCompressionLevel zstd.EncoderLevel,
	brotliCompressionLevel int,
	preferEncoder config.Encoding,
	logger *zap.Logger,
) func(http.Handler) http.Handler {
	re := regexp2.MustCompile(filePattern)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := newResponseRecorder(r.URL.Path)

			next.ServeHTTP(rec, r)

			encodedBody, encoding, ok := encode(rec, r, re, zstdCompressionLevel, brotliCompressionLevel, preferEncoder, cacheStore, store, logger)
			if !ok {
				_ = rec.flush(w)
				return
			}

			writeEncodedResponse(w, rec, encoding, encodedBody)
		})
	}
}

// encode выполняет все проверки и возвращает сжатое тело ответа.
// Если сжатие невозможно, возвращает ok=false (вызывающий код должен сделать flush).
func encode(
	rec *responseRecorder,
	r *http.Request,
	re *regexp2.Regexp,
	zstdCompressionLevel zstd.EncoderLevel,
	brotliCompressionLevel int,
	preferEncoder config.Encoding,
	cacheStore *CompressorCache,
	store storage.Storage,
	logger *zap.Logger,
) ([]byte, config.Encoding, bool) {
	if reason, ok := isValidRequest(rec, r, re); !ok {
		logger.Debug("skipping encoding", zap.String("reason", reason), zap.String("path", r.URL.Path))
		return nil, "", false
	}

	if err := addHeaders(rec, r, re); err != nil {
		logger.Error("error adding headers", zap.Error(err), zap.String("path", r.URL.Path))
		return nil, "", false
	}

	// Выбираем кодирование, которое поддерживает клиент: preferred имеет приоритет, затем fallback.
	acceptEncoding := r.Header.Get("Accept-Encoding")
	encoding, ok := negotiateEncoding(acceptEncoding, preferEncoder)
	if !ok {
		logger.Debug("skipping encoding",
			zap.String("reason", "Accept-Encoding does not contain supported encoding"),
			zap.String("preferEncoder", string(preferEncoder)),
			zap.String("acceptEncoding", acceptEncoding))
		return nil, "", false
	}

	// Получаем хэш словаря
	adValue := r.Header.Get("Available-Dictionary")
	if !isValidADHeaderFormat(adValue) {
		logger.Debug("skipping encoding",
			zap.String("reason", "Available-Dictionary header format is invalid"),
			zap.String("availableDict", adValue))
		return nil, "", false
	}
	adHash := extractAvailableDictionary(adValue)

	compressor, err := getCompressor(adHash, zstdCompressionLevel, brotliCompressionLevel, encoding, cacheStore, store)
	if err != nil {
		logCompressorError(logger, err, adHash, r.URL.Path)
		return nil, "", false
	}

	encodedBody := compressor.Compress(rec.body.Bytes())
	if len(encodedBody) == 0 {
		logger.Error("error compressing response", zap.String("path", r.URL.Path), zap.String("encoding", string(encoding)))
		return nil, "", false
	}

	return encodedBody, encoding, true
}

func negotiateEncoding(acceptEncoding string, preferEncoder config.Encoding) (config.Encoding, bool) {
	encodings := preferredEncodings(preferEncoder)
	for _, encoding := range encodings {
		if acceptsEncoding(acceptEncoding, encoding) {
			return encoding, true
		}
	}
	return "", false
}

func preferredEncodings(preferEncoder config.Encoding) []config.Encoding {
	switch preferEncoder {
	case config.DCB:
		return []config.Encoding{config.DCB, config.DCZ}
	default:
		return []config.Encoding{config.DCZ, config.DCB}
	}
}

func acceptsEncoding(acceptEncoding string, encoding config.Encoding) bool {
	for token := range strings.SplitSeq(acceptEncoding, ",") {
		parts := strings.Split(strings.TrimSpace(token), ";")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) != string(encoding) {
			continue
		}

		accepted := true
		for _, part := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || strings.TrimSpace(key) != "q" {
				continue
			}
			q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			accepted = err != nil || q > 0
		}
		return accepted
	}
	return false
}

// logCompressorError логирует ошибку получения компрессора с подходящим уровнем.
func logCompressorError(logger *zap.Logger, err error, adHash, path string) {
	if errors.Is(err, storage.ErrNotExists) {
		logger.Debug("skipping encoding",
			zap.String("reason", "dictionary not found in storage"),
			zap.String("hash", adHash))
		return
	}
	logger.Error(
		"error getting compressor",
		zap.Error(err),
		zap.String("hash", adHash),
		zap.String("path", path),
	)
}

// writeEncodedResponse копирует заголовки из recorder, добавляет заголовки кодирования и отправляет сжатое тело.
func writeEncodedResponse(w http.ResponseWriter, rec *responseRecorder, encoding config.Encoding, encodedBody []byte) {
	for key, values := range rec.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Encoding", string(encoding))
	w.Header().Set("Content-Length", strconv.Itoa(len(encodedBody)))

	w.WriteHeader(rec.statusCode)
	// #nosec G705 -- writing compressed binary payload, not interpretable as HTML
	_, _ = w.Write(encodedBody)
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
func isValidRequest(rec *responseRecorder, r *http.Request, re *regexp2.Regexp) (string, bool) {
	// Отсеиваем, если содержимое уже сжато
	if len(rec.header.Get("Content-Encoding")) != 0 {
		return "content already compressed", false
	}

	// Отсеиваем, если Cache-Control не кеширует файл
	if !slices.Contains(strings.Split(rec.header.Get("Cache-Control"), ", "), "public") {
		return "Cache-Control is not public", false
	}

	// Получаем Content-Type
	contentType := rec.header.Get("Content-Type")

	mimeType := contentType
	if before, _, ok := strings.Cut(contentType, ";"); ok {
		mimeType = before
	}
	mimeType = strings.TrimSpace(mimeType)

	// Проверяем, допускает ли MIME-тип сжатие
	if !isMimeAllowed(mimeType) {
		return "MIME type not allowed", false
	}

	// Проверяем regex файла
	_, filename := path.Split(r.URL.Path)
	match, err := re.MatchString(filename)
	if err != nil {
		return fmt.Sprintf("error: %v", err), false
	}
	if !match {
		return "filename does not match regex pattern", false
	}

	return "", true
}

// addHeaders adds the `Use-As-Dictionary` and `Vary` headers to the response.
func addHeaders(rec *responseRecorder, r *http.Request, re *regexp2.Regexp) error {
	match, err := getMatchPattern(r.URL.Path, re)
	if err != nil {
		return err
	}
	rec.header.Set("Use-As-Dictionary", fmt.Sprintf(`match="%s"`, match))

	rec.header.Set("Vary", "Available-Dictionary, Accept-Encoding")

	return nil
}

// getMatchPattern builds a glob-style match pattern from pathStr using the first regex match in the filename.
func getMatchPattern(pathStr string, re *regexp2.Regexp) (string, error) {
	dir, filename := path.Split(pathStr)

	foundMatch, err := re.FindStringMatch(filename)
	if err != nil {
		return "", err
	}
	if foundMatch == nil {
		return "", errors.New("regex match returned no result")
	}

	// Разбиваем filename по первому вхождению matched-подстроки,
	// каждую часть экранируем, между ними вставляем "*".
	before, after, _ := strings.Cut(filename, foundMatch.String())
	return dir + escapeGlob(before) + "*" + escapeGlob(after), nil
}

func getCompressor(
	hashDict string,
	zstdCompressionLevel zstd.EncoderLevel,
	brotliCompressionLevel int,
	encoding config.Encoding,
	cacheStore *CompressorCache,
	storage storage.Storage,
) (delta.Compressor, error) {
	cacheKey := fmt.Sprintf("%s:zstd=%d:brotli=%d:%s", encoding, zstdCompressionLevel, brotliCompressionLevel, hashDict)
	if compressor, ok := cacheStore.Get(cacheKey); ok {
		return compressor, nil
	}

	dictionary, err := storage.Pull(hashDict)
	if err != nil {
		return nil, err
	}

	var compressor delta.Compressor
	switch encoding {
	case config.DCZ:
		compressor, err = dcz.NewCompressor(dictionary, zstdCompressionLevel)
	case config.DCB:
		if brotliCompressionLevel < 2 || brotliCompressionLevel > 11 {
			return nil, fmt.Errorf("brotli dictionary compression level must be between 2 and 11: %d", brotliCompressionLevel)
		}
		compressor, err = dcb.NewCompressor(dictionary, brotliCompressionLevel)
	default:
		return nil, fmt.Errorf("unsupported encoding: %s", encoding)
	}
	if err != nil {
		return nil, err
	}

	go func(cacheKey string, compressor delta.Compressor) {
		cacheStore.Add(cacheKey, compressor)
	}(cacheKey, compressor)

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
