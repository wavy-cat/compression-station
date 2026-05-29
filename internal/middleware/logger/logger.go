package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

func Logger(logger *zap.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			t1 := time.Now()
			defer func() {
				logger.Info("HTTP request",
					zap.Dict("request",
						zap.String("url", r.URL.String()),
						zap.String("method", r.Method),
						zap.String("proto", r.Proto),
						zap.String("userAgent", r.UserAgent())),
					zap.Dict("response",
						zap.Int("status", ww.Status()),
						zap.Int("contentLength", ww.BytesWritten()),
						zap.Duration("elapsed", time.Since(t1))),
				)
			}()

			next.ServeHTTP(ww, r.WithContext(ctx))
		})
	}
}
