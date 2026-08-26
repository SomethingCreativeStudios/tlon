// Package server exposes Tlon's generated strict OGC API Records HTTP server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/SomethingCreativeStudios/tlon/api"
	"github.com/SomethingCreativeStudios/tlon/application"
	"github.com/SomethingCreativeStudios/tlon/auth"
	"github.com/SomethingCreativeStudios/tlon/internal/cursor"
	recorddoc "github.com/SomethingCreativeStudios/tlon/internal/record"
	"github.com/SomethingCreativeStudios/tlon/store"
)

type Options struct {
	DefaultLimit int
	MaximumLimit int
	Logger       *slog.Logger
}

type Service struct {
	app          *application.Application
	cursors      *cursor.Codec
	publicURL    string
	defaultLimit int
	maximumLimit int
	logger       *slog.Logger
}

type requestContextKey struct{}

func New(app *application.Application, cursorSecret string, options Options) (http.Handler, error) {
	if app == nil || app.Store == nil {
		return nil, errors.New("application and store are required")
	}
	publicURL, err := url.Parse(app.PublicURL)
	if err != nil || (publicURL.Scheme != "http" && publicURL.Scheme != "https") || publicURL.Host == "" {
		return nil, errors.New("public URL must be an absolute HTTP(S) URL")
	}
	codec, err := cursor.New(cursorSecret)
	if err != nil {
		return nil, err
	}
	if options.DefaultLimit == 0 {
		options.DefaultLimit = 10
	}
	if options.DefaultLimit < 0 {
		return nil, errors.New("default limit must be positive")
	}
	if options.MaximumLimit < 1 {
		options.MaximumLimit = 1000
	}
	if options.DefaultLimit > options.MaximumLimit {
		return nil, errors.New("default limit exceeds maximum limit")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	s := &Service{app: app, cursors: codec, publicURL: strings.TrimRight(app.PublicURL, "/"), defaultLimit: options.DefaultLimit, maximumLimit: options.MaximumLimit, logger: options.Logger}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP)
	r.Use(s.requestContext, s.recoverer, s.accessLog, s.headContentType)
	r.Handle("/playground", playgroundHandler())
	r.Handle("/playground/*", playgroundHandler())
	strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{RequestErrorHandlerFunc: s.requestError, ResponseErrorHandlerFunc: s.responseError})
	return api.HandlerFromMux(strict, r), nil
}

func (s *Service) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestContextKey{}, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func requestFrom(ctx context.Context) *http.Request {
	request, _ := ctx.Value(requestContextKey{}).(*http.Request)
	return request
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}
func (s *Service) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		s.logger.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes, "duration", time.Since(started), "request_id", middleware.GetReqID(r.Context()))
	})
}
func (s *Service) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.ErrorContext(r.Context(), "panic", "error", value, "stack", string(debug.Stack()))
				writeProblem(w, r, http.StatusInternalServerError, "InternalServerError", "Internal Server Error", "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Service) headContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			switch {
			case r.URL.Path == "/playground" || r.URL.Path == "/playground/":
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			case strings.HasPrefix(r.URL.Path, "/playground/") && strings.HasSuffix(r.URL.Path, ".js"):
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			case strings.HasPrefix(r.URL.Path, "/playground/") && strings.HasSuffix(r.URL.Path, ".css"):
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			case strings.HasSuffix(r.URL.Path, "/items") || strings.Contains(r.URL.Path, "/items/"):
				w.Header().Set("Content-Type", "application/geo+json")
			case r.URL.Path == "/api":
				w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.0")
			case strings.HasSuffix(r.URL.Path, "/queryables"), strings.HasSuffix(r.URL.Path, "/sortables"), strings.HasSuffix(r.URL.Path, "/schema"):
				w.Header().Set("Content-Type", "application/schema+json")
			case strings.HasSuffix(r.URL.Path, "/facets"):
				w.Header().Set("Content-Type", "application/facets+json")
			default:
				w.Header().Set("Content-Type", "application/json")
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) requestError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.DebugContext(r.Context(), "invalid request", "error", err)
	writeProblem(w, r, http.StatusBadRequest, "InvalidParameterValue", "Bad Request", err.Error())
}
func (s *Service) responseError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "response error", "error", err)
	writeProblem(w, r, http.StatusInternalServerError, "InternalServerError", "Internal Server Error", "could not encode the response")
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	instance := ""
	if r != nil {
		instance = r.URL.RequestURI()
	}
	_ = json.NewEncoder(w).Encode(api.Problem{Type: "about:blank", Title: title, Status: status, Detail: &detail, Instance: &instance, Code: code, Description: detail})
}

func (s *Service) problem(ctx context.Context, err error) (api.Problem, int) {
	status, code, title := http.StatusInternalServerError, "InternalServerError", "Internal Server Error"
	switch {
	case errors.Is(err, store.ErrNotFound):
		status, code, title = http.StatusNotFound, "NotFound", "Not Found"
	case errors.Is(err, store.ErrConflict):
		status, code, title = http.StatusConflict, "DuplicateRecord", "Conflict"
	case errors.Is(err, store.ErrPrecondition):
		status, code, title = http.StatusPreconditionFailed, "PreconditionFailed", "Precondition Failed"
	case errors.Is(err, auth.ErrForbidden):
		status, code, title = http.StatusForbidden, "Forbidden", "Forbidden"
	case errors.Is(err, recorddoc.ErrInvalid), errors.Is(err, cursor.ErrInvalid):
		status, code, title = http.StatusBadRequest, "InvalidParameterValue", "Bad Request"
	default:
		if isClientError(err) {
			status, code, title = http.StatusBadRequest, "InvalidParameterValue", "Bad Request"
		} else {
			s.logger.ErrorContext(ctx, "request failed", "error", err)
		}
	}
	detail := err.Error()
	instance := ""
	if request := requestFrom(ctx); request != nil {
		instance = request.URL.RequestURI()
	}
	return api.Problem{Type: "about:blank", Title: title, Status: status, Detail: &detail, Instance: &instance, Code: code, Description: detail}, status
}
func isClientError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, prefix := range []string{"invalid ", "unknown ", "property ", "bbox ", "datetime:", "at most ", "cursor ", "facets "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

var _ api.StrictServerInterface = (*Service)(nil)
