package fetcher

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

func Fetcher(originURL *url.URL) (http.HandlerFunc, error) {
	proxy := httputil.NewSingleHostReverseProxy(originURL)
	return proxy.ServeHTTP, nil
}
