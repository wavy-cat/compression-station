package content

import (
	"crypto/sha256"
	"encoding/base64"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"slices"
)

const (
	mimeApplicationJavaScript = "application/javascript"
	mimeApplicationXML        = "application/xml"
	mimeTextHTML              = "text/html"
	mimeTextMarkdown          = "text/x-markdown"
)

var AllowedMimeTypes = []string{
	mimeTextHTML,
	"text/richtext",
	"text/plain",
	"text/css",
	"text/x-script",
	"text/x-component",
	"text/x-java-source",
	mimeTextMarkdown,
	mimeApplicationJavaScript,
	"application/x-javascript",
	"text/javascript",
	"text/js",
	"image/x-icon",
	"image/vnd.microsoft.icon",
	"application/x-perl",
	"application/x-httpd-cgi",
	"text/xml",
	mimeApplicationXML,
	"application/rss+xml",
	"application/vnd.api+json",
	"application/x-protobuf",
	"application/json",
	"multipart/bag",
	"multipart/mixed",
	"application/xhtml+xml",
	"font/ttf",
	"font/otf",
	"font/x-woff",
	"image/svg+xml",
	"application/vnd.ms-fontobject",
	"application/ttf",
	"application/x-ttf",
	"application/otf",
	"application/x-otf",
	"application/truetype",
	"application/opentype",
	"application/x-opentype",
	"application/font-woff",
	"application/eot",
	"application/font",
	"application/font-sfnt",
	"application/wasm",
	"application/javascript-binast",
	"application/manifest+json",
	"application/ld+json",
	"application/graphql+json",
	"application/geo+json",
}

var extensionMimeTypes = map[string]string{
	".css":      "text/css",
	".eot":      "application/vnd.ms-fontobject",
	".geojson":  "application/geo+json",
	".graphql":  "application/graphql+json",
	".htm":      mimeTextHTML,
	".html":     mimeTextHTML,
	".ico":      "image/x-icon",
	".js":       mimeApplicationJavaScript,
	".json":     "application/json",
	".md":       mimeTextMarkdown,
	".markdown": mimeTextMarkdown,
	".mjs":      mimeApplicationJavaScript,
	".otf":      "font/otf",
	".pl":       "application/x-perl",
	".proto":    "application/x-protobuf",
	".rdf":      mimeApplicationXML,
	".rss":      "application/rss+xml",
	".svg":      "image/svg+xml",
	".ttf":      "font/ttf",
	".txt":      "text/plain",
	".wasm":     "application/wasm",
	".woff":     "font/x-woff",
	".xhtml":    "application/xhtml+xml",
	".xml":      mimeApplicationXML,
	".xsl":      "text/xml",
}

func NormalizeMimeType(value string) string {
	if before, _, ok := strings.Cut(value, ";"); ok {
		value = before
	}
	return strings.TrimSpace(value)
}

func IsAllowedMimeType(mimeType string) bool {
	return slices.Contains(AllowedMimeTypes, NormalizeMimeType(mimeType))
}

func DetectMimeType(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	if mimeType, ok := extensionMimeTypes[ext]; ok {
		return mimeType
	}
	if ext != "" {
		if mimeType := mime.TypeByExtension(ext); mimeType != "" {
			return NormalizeMimeType(mimeType)
		}
	}

	return NormalizeMimeType(http.DetectContentType(data))
}

func HashName(data []byte) string {
	sum := sha256.Sum256(data)
	encoded := base64.StdEncoding.EncodeToString(sum[:])
	return strings.NewReplacer("/", "+", "+", "-").Replace(encoded)
}
