package config

type LoggerPreset string

const (
	ProdPreset LoggerPreset = "prod"
	DevPreset  LoggerPreset = "dev"
)

type StorageType string

const (
	Local StorageType = "local"
	S3    StorageType = "s3"
)

type Encoding string

const (
	DCZ Encoding = "dcz"
	DCB Encoding = "dcb"
)

type CacheType string

const (
	Memory CacheType = "memory"
	Redis  CacheType = "redis"
)
