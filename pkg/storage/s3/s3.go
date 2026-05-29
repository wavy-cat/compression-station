package s3

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/klauspost/compress/zstd"
	"github.com/wavy-cat/compression-station/pkg/storage"
)

type Storage struct {
	client *s3.Client
	bucket string
	prefix string
}

type Config struct {
	Bucket      string
	Region      string
	Endpoint    string
	Prefix      string
	AccessToken string
	SecretToken string
}

func NewStorage(ctx context.Context, cfg Config) (storage.Storage, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}

	var loadOptions []func(*awsconfig.LoadOptions) error

	if cfg.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(cfg.Region))
	}

	if cfg.AccessToken != "" || cfg.SecretToken != "" {
		if cfg.AccessToken == "" {
			return nil, errors.New("s3 access token is required when secret token is set")
		}
		if cfg.SecretToken == "" {
			return nil, errors.New("s3 secret token is required when access token is set")
		}

		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessToken, cfg.SecretToken, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}

	var clientOptions []func(*s3.Options)

	if cfg.Endpoint != "" {
		clientOptions = append(clientOptions, func(options *s3.Options) {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
			options.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, clientOptions...)

	return &Storage{
		client: client,
		bucket: cfg.Bucket,
		prefix: cfg.Prefix,
	}, nil
}

func (s *Storage) Push(key string, value []byte) error {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return err
	}
	defer encoder.Close()

	compressed := encoder.EncodeAll(value, nil)

	_, err = s.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
		Body:   bytes.NewReader(compressed),
	})

	return err
}

func (s *Storage) Pull(key string) ([]byte, error) {
	output, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if _, ok := errors.AsType[*s3types.NoSuchKey](err); ok {
			return nil, storage.ErrNotExists
		}

		return nil, err
	}
	defer output.Body.Close()

	data, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, err
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	decompressed, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, err
	}

	return decompressed, nil
}

func (s *Storage) Close() error {
	return nil
}

func (s *Storage) objectKey(key string) string {
	if s.prefix == "" {
		return key
	}

	return s.prefix + "/" + key
}
