package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
)

// Server is the external REST API server.
type Server struct {
	httpServer *http.Server
	cfg        config.APIConfig
	logger     *slog.Logger

	// limiters tracks per-key rate limiter state.
	limiters   map[string]*rateLimiter
	limitersMu sync.Mutex
}

// New creates a new API server. The server is not started until Run is called.
func New(cfg config.APIConfig, logger *slog.Logger) *Server {
	s := &Server{
		cfg:      cfg,
		logger:   logger,
		limiters: make(map[string]*rateLimiter),
	}

	mux := http.NewServeMux()

	// Health endpoint — no auth required.
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// All other routes go through auth middleware.
	// (Endpoints will be added in subsequent PRs.)

	var handler http.Handler = mux
	handler = s.middlewareLogging(handler)
	handler = s.middlewareCORS(handler)
	handler = s.middlewareRecover(handler)

	s.httpServer = &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// Run starts the server and blocks until ctx is cancelled. It performs a
// graceful shutdown with a 5-second deadline.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", s.cfg.Listen, err)
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("api server starting", "addr", s.cfg.Listen, "tls", s.cfg.TLS)
		if s.cfg.TLS {
			errCh <- s.httpServer.ServeTLS(ln, s.cfg.CertFile, s.cfg.KeyFile)
		} else {
			errCh <- s.httpServer.Serve(ln)
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("api server shutdown error", "error", err)
			return fmt.Errorf("api: shutdown: %w", err)
		}
		s.logger.Info("api server stopped")
		return nil
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("api: serve: %w", err)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Auth middleware
// ---------------------------------------------------------------------------

// contextKey is an unexported type used for context value keys.
type contextKey string

const keyNameKey contextKey = "api_key_name"

// RequireScope returns middleware that checks for a valid API key with the
// required scope. Use this to wrap individual route handlers.
func (s *Server) RequireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keyName, ok := s.authenticate(r, scope)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		// Rate limiting (per key).
		if s.cfg.RateLimit > 0 {
			if !s.allowRequest(keyName) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
				return
			}
		}

		ctx := context.WithValue(r.Context(), keyNameKey, keyName)
		next(w, r.WithContext(ctx))
	}
}

// authenticate checks the Authorization header for a valid API key with the
// given scope. Returns the key name and true if valid.
func (s *Server) authenticate(r *http.Request, scope string) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	token := strings.TrimPrefix(header, "Bearer ")
	if token == header {
		return "", false // no "Bearer " prefix
	}

	for _, k := range s.cfg.Keys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(k.Key)) == 1 {
			for _, s := range k.Scopes {
				if s == scope {
					return k.Name, true
				}
			}
			return "", false // key valid but scope missing
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Rate limiter (token bucket, per-key)
// ---------------------------------------------------------------------------

type rateLimiter struct {
	mu       sync.Mutex
	tokens   float64
	maxRate  float64
	lastTime time.Time
}

func (s *Server) allowRequest(keyName string) bool {
	s.limitersMu.Lock()
	rl, ok := s.limiters[keyName]
	if !ok {
		rl = &rateLimiter{
			tokens:   s.cfg.RateLimit,
			maxRate:  s.cfg.RateLimit,
			lastTime: time.Now(),
		}
		s.limiters[keyName] = rl
	}
	s.limitersMu.Unlock()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastTime).Seconds()
	rl.tokens += elapsed * rl.maxRate
	if rl.tokens > rl.maxRate {
		rl.tokens = rl.maxRate
	}
	rl.lastTime = now

	if rl.tokens < 1 {
		return false
	}
	rl.tokens--
	return true
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func (s *Server) middlewareLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.logger.Info("api request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

func (s *Server) middlewareCORS(next http.Handler) http.Handler {
	if len(s.cfg.CORSOrigins) == 0 {
		return next
	}
	allowed := make(map[string]bool, len(s.cfg.CORSOrigins))
	for _, o := range s.cfg.CORSOrigins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) middlewareRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("api panic recovered", "panic", rec, "path", r.URL.Path)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
