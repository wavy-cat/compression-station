package dcz

import (
	"crypto/sha256"
	"fmt"

	"github.com/klauspost/compress/zstd"
	"github.com/wavy-cat/compression-station/pkg/delta"
)

var dczMagic = []byte{0x5e, 0x2a, 0x4d, 0x18, 0x20, 0x00, 0x00, 0x00}

const (
	dczMagicLen = 8
	dictHashLen = 32
)

type compressor struct {
	encoder *zstd.Encoder
	header  []byte // magic + dictHash, вычисляется один раз
}

func NewCompressor(dict []byte, level zstd.EncoderLevel) (delta.Compressor, error) {
	encoder, err := zstd.NewWriter(nil,
		zstd.WithEncoderDictRaw(0, dict),
		zstd.WithEncoderLevel(level),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build compression dictionary: %w", err)
	}

	dictHash := sha256.Sum256(dict)
	header := make([]byte, 0, dczMagicLen+dictHashLen)
	header = append(header, dczMagic...)
	header = append(header, dictHash[:]...)

	return &compressor{encoder: encoder, header: header}, nil
}

func (c *compressor) Compress(content []byte) []byte {
	dst := make([]byte, len(c.header), len(c.header)+len(content))
	copy(dst, c.header)
	return c.encoder.EncodeAll(content, dst)
}

func (c *compressor) Release() {
	_ = c.encoder.Close()
}
