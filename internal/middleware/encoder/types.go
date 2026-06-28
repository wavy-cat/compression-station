package encoder

import (
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/wavy-cat/compression-station/pkg/delta"
)

type CompressorCache = lru.Cache[string, delta.Compressor] // Use with lru.NewWithEvict()
