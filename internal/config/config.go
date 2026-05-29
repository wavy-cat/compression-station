package config

import (
	"time"

	"github.com/klauspost/compress/zstd"
)

type Server struct {
	Host            string        `yaml:"host" env:"HOST" env-default:"127.0.0.1"`
	Port            uint16        `yaml:"port" env:"PORT" env-default:"3399"`
	Heartbeat       Heartbeat     `yaml:"heartbeat"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT" env-default:"10s"`
}

type Heartbeat struct {
	Enable bool   `yaml:"enable" env:"ENABLE" env-default:"false"`
	Path   string `yaml:"path" env:"PATH" env-default:"/health"`
}

type Logger struct {
	Preset LoggerPreset `yaml:"preset" env:"LOGGER_PRESET" env-default:"prod"`
	Level  string       `yaml:"level" env:"LOGGER_LEVEL" env-default:"info"`
}

type Encoder struct {
	FilePattern      string            `yaml:"file_pattern" env:"FILE_PATTERN" env-required:"true"` // регулярное выражение, которому должно соответствовать полное название файла, чтобы быть обработанным
	Paths            []string          `yaml:"paths" env:"PATHS" env-required:"true"`               // начало путей директорий, которые должны быть обработаны (e.g. `/_astro`, `/css`)
	CompressionLevel zstd.EncoderLevel `yaml:"compression_level" env:"COMPRESSION_LEVEL" env-default:"2"`
} // TODO: добавить excluded paths

type Storage struct {
	StorageType StorageType  `yaml:"type" env:"STORAGE_TYPE" env-default:"local"`
	Local       StorageLocal `yaml:"local"`
	S3          StorageS3    `yaml:"s3"`
}

type StorageLocal struct {
	DirectoryPath string `yaml:"directory_path" env:"DIRECTORY_PATH" env-default:"./storage"`
}

type StorageS3 struct {
	Bucket      string `yaml:"bucket" env:"BUCKET"`
	Region      string `yaml:"region" env:"REGION"`
	Endpoint    string `yaml:"endpoint" env:"ENDPOINT"`
	Prefix      string `yaml:"prefix" env:"PREFIX"`
	AccessToken string `yaml:"access_token" env:"ACCESS_TOKEN"`
	SecretToken string `yaml:"secret_token" env:"SECRET_TOKEN"`
}

type Cache struct {
	Size int `yaml:"size" env:"CACHE_SIZE" env-default:"1024"`
}

type CacheRedis struct {
	Address  string `yaml:"address" env:"CACHE_REDIS_ADDRESS"`
	Password string `yaml:"password" env:"CACHE_REDIS_PASSWORD"`
	DB       int    `yaml:"db" env:"CACHE_REDIS_DB" env-default:"0"`
}

type Origin struct {
	Url string `yaml:"url" env:"ORIGIN_URL" env-required:"true"`
}

type Config struct {
	Server
	Logger
	Encoder
	Storage
	Cache
	Origin
}
