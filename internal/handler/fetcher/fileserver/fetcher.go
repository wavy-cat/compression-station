package fileserver

import (
	"fmt"
	"net/http"
	"os"
)

func Fetcher(path string) (http.HandlerFunc, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file server root %q: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file server root %q is not a directory", path)
	}

	return http.FileServer(http.Dir(path)).ServeHTTP, nil
}
