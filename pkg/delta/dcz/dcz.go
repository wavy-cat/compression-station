package dcz

import (
	"crypto/sha256"
	"fmt"

	"github.com/valyala/gozstd"
	"github.com/wavy-cat/compression-station/pkg/delta"
)

var dczMagic = []byte{0x5e, 0x2a, 0x4d, 0x18, 0x20, 0x00, 0x00, 0x00}

const (
	dczMagicLen = 8
	dictHashLen = 32
)

type compressor struct {
	cdict  *gozstd.CDict
	header []byte // magic + dictHash, вычисляется один раз
}

func NewCompressor(dict []byte, level int) (delta.Compressor, error) {
	cdict, err := gozstd.NewCDictLevel(dict, level)
	if err != nil {
		return nil, fmt.Errorf("failed to build compression dictionary: %v", err)
	}

	dictHash := sha256.Sum256(dict)
	header := make([]byte, 0, dczMagicLen+dictHashLen)
	header = append(header, dczMagic...)
	header = append(header, dictHash[:]...)

	return &compressor{cdict: cdict, header: header}, nil
}

func (c *compressor) Compress(content []byte) []byte {
	return gozstd.CompressDict(c.header, content, c.cdict)
}

func (c *compressor) Release() {
	c.cdict.Release()
}
