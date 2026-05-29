package config

import (
	"net/url"

	"gopkg.in/yaml.v3"
)

// URL — обёртка над [url.URL] для корректного парсинга из YAML и переменных окружения.
type URL struct {
	url.URL
}

// UnmarshalYAML реализует разбор строки в URL для yaml.v3.
func (u *URL) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	return u.parse(raw)
}

// SetValue реализует интерфейс cleanenv.Setter для разбора из переменной окружения.
func (u *URL) SetValue(s string) error {
	return u.parse(s)
}

func (u *URL) parse(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	u.URL = *parsed
	return nil
}
