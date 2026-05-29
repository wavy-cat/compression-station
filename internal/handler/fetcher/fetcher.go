package fetcher

import (
	"io"
	"net/http"
	"strings"
)

// Fetcher запрашивает контент у origin и возвращает ответ в неизменном виде
func Fetcher(originURL string) http.HandlerFunc {
	client := &http.Client{}
	originURL = strings.TrimRight(originURL, "/")

	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := originURL + r.URL.RequestURI()

		req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		req.Header = r.Header.Clone()

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		//goland:noinspection ALL
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		w.WriteHeader(resp.StatusCode)

		if _, err := io.Copy(w, resp.Body); err != nil {
			return
		}
	}
}
