package dcz

import (
	"crypto/sha256"
	"fmt"

	"github.com/valyala/gozstd"
)

var dczMagic = []byte{0x5e, 0x2a, 0x4d, 0x18, 0x20, 0x00, 0x00, 0x00}

const (
	dczMagicLen = 8
	dictHashLen = 32
)

func CompressDict(dict []byte, content []byte) ([]byte, error) {
	cdict, err := gozstd.NewCDict(dict) // TODO: Добавить level
	if err != nil {
		return nil, fmt.Errorf("failed to build compression dictionary: %v", err)
	}
	defer cdict.Release()

	compressedBundle := gozstd.CompressDict(nil, content, cdict)

	dictHash := sha256.Sum256(dict)

	body := make([]byte, 0, dczMagicLen+dictHashLen+len(compressedBundle))
	body = append(body, dczMagic...)
	body = append(body, dictHash[:]...)
	body = append(body, compressedBundle...)

	return body, nil
}
