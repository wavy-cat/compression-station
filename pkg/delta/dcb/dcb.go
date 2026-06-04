package dcb

import (
	"bytes"
	"crypto/sha256"
	"fmt"

	brrr "github.com/molecule-man/go-brrr"
	"github.com/wavy-cat/compression-station/pkg/delta"
)

var dcbMagic = []byte{0xff, 0x44, 0x43, 0x42}

const (
	dcbMagicLen = 4
	dictHashLen = 32
)

type compressor struct {
	dictionary       *brrr.PreparedDictionary
	compressionLevel int
	header           []byte
}

func NewCompressor(dict []byte, level int) (delta.Compressor, error) {
	preparedDictionary, err := brrr.PrepareDictionary(dict)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare brotli dictionary: %w", err)
	}

	dictHash := sha256.Sum256(dict)
	header := make([]byte, 0, dcbMagicLen+dictHashLen)
	header = append(header, dcbMagic...)
	header = append(header, dictHash[:]...)

	return &compressor{
		dictionary:       preparedDictionary,
		compressionLevel: level,
		header:           header,
	}, nil
}

func (c *compressor) Compress(content []byte) []byte {
	dst := bytes.NewBuffer(make([]byte, 0, len(c.header)+len(content)))
	dst.Write(c.header)
	writer, err := brrr.NewWriterOptions(dst, c.compressionLevel, brrr.WriterOptions{
		Dictionaries: []*brrr.PreparedDictionary{c.dictionary},
		SizeHint:     uint(len(content)),
	})
	if err != nil {
		return nil
	}

	if _, err = writer.Write(content); err != nil {
		return nil
	}
	if err = writer.Close(); err != nil {
		return nil
	}

	return dst.Bytes()
}

func (c *compressor) Release() {}
