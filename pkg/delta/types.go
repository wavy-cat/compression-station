package delta

type Compressor interface {
	Compress(content []byte) []byte
	Release()
}
