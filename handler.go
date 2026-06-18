package main

import (
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	reUUID  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
)


type server struct {
	cfg     *Config
	storage *Storage
	// accessLog returns the current access logger. It is called on every
	// request so log rotation (which replaces the underlying logger) is picked
	// up without restart. If nil, slog.Default() is used.
	accessLog func() *slog.Logger
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// clientAddr returns the originating client address. It honors X-Forwarded-For
// (set by the upstream reverse proxy, e.g. Caddy) and falls back to
// r.RemoteAddr when the header is absent. The server is intended to run
// behind a trusted proxy on localhost, so X-Forwarded-For from the wire is
// not directly attacker-controllable.
func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For is "client, proxy1, proxy2, ..."; the first
		// entry is the original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log := slog.Default()
		if s.accessLog != nil {
			log = s.accessLog()
		}
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"location", rec.Header().Get("Location"),
			"remote_addr", clientAddr(r),
			"user_agent", r.UserAgent(),
			"accept_encoding", r.Header.Get("Accept-Encoding"),
		)
	})
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/", s.handleResource)
	mux.HandleFunc("/package/", s.handleResource)
	mux.HandleFunc("/artifact/", s.handleResource)
	mux.HandleFunc("/registries", s.handleRegistries)
	return s.logRequests(mux)
}

// encodingQ returns the effective q value for enc in an Accept-Encoding header.
// Handles explicit entries, the * wildcard, and an absent header (q=1 for all).
// Returns -1 if the encoding is not acceptable.
func encodingQ(header, enc string) float64 {
	if header == "" {
		return 1.0
	}
	wildcard := -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		name, q := part, 1.0
		if i := strings.IndexByte(part, ';'); i >= 0 {
			name = strings.TrimSpace(part[:i])
			param := strings.TrimSpace(part[i+1:])
			if len(param) > 2 && strings.EqualFold(param[:2], "q=") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64); err == nil {
					q = v
				}
			}
		}
		switch strings.ToLower(name) {
		case enc:
			return q // explicit entry always wins
		case "*":
			wildcard = q
		}
	}
	if wildcard >= 0 {
		return wildcard
	}
	return -1
}

// preferredEncodings returns the storage extensions and Content-Encoding values
// the client accepts, in preference order (highest q first).
// Returns nil if no supported encoding is acceptable; caller should respond 406.
func preferredEncodings(header string) (exts, encs []string) {
	zq := encodingQ(header, "zstd")
	gq := encodingQ(header, "gzip")

	// Emit in descending q order; zstd wins ties (it's our preferred encoding).
	if zq > 0 && zq >= gq {
		exts = append(exts, ".tar.zst")
		encs = append(encs, "zstd")
	}
	if gq > 0 {
		exts = append(exts, ".tar.gz")
		encs = append(encs, "gzip")
	}
	if zq > 0 && zq < gq {
		exts = append(exts, ".tar.zst")
		encs = append(encs, "zstd")
	}
	return
}

// canonicalExt strips a compression extension from s and returns the
// canonical storage extension (.tar.gz or .tar.zst) and the bare name.
// Returns ("", s) if no known extension is present.
func canonicalExt(s string) (ext, base string) {
	for _, sfx := range []string{".tar.zst", ".zst"} {
		if strings.HasSuffix(s, sfx) {
			return ".tar.zst", s[:len(s)-len(sfx)]
		}
	}
	for _, sfx := range []string{".tar.gz", ".gz"} {
		if strings.HasSuffix(s, sfx) {
			return ".tar.gz", s[:len(s)-len(sfx)]
		}
	}
	return "", s
}

// parseResourcePath validates and normalises a resource URL path.
// Returns the bare storage path (no extension) and the canonical extension
// (".tar.gz" or ".tar.zst"), or ("", "", false) if the path is invalid.
func parseResourcePath(urlPath string) (path, ext string, ok bool) {
	parts := strings.Split(strings.TrimLeft(urlPath, "/"), "/")

	// Strip extension from the last component.
	last := parts[len(parts)-1]
	ext, parts[len(parts)-1] = canonicalExt(last)

	switch parts[0] {
	case "registry", "package":
		if len(parts) != 3 {
			return "", "", false
		}
		if !reUUID.MatchString(parts[1]) || !reHex40.MatchString(parts[2]) {
			return "", "", false
		}
	case "artifact":
		if len(parts) != 2 {
			return "", "", false
		}
		if !reHex40.MatchString(parts[1]) {
			return "", "", false
		}
	default:
		return "", "", false
	}
	return strings.Join(parts, "/"), ext, true
}

func (s *server) handleResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path, urlExt, ok := parseResourcePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	// URL extension takes precedence; fall back to Accept-Encoding negotiation.
	var exts []string
	switch urlExt {
	case ".tar.zst":
		exts = []string{".tar.zst"}
	case ".tar.gz":
		exts = []string{".tar.gz"}
	default:
		ae := r.Header.Get("Accept-Encoding")
		if ae == "" {
			// For historical reasons we default to .tar.gz if the client
			// doesn't pass Accept-Encoding
			exts = []string{".tar.gz"}
		} else {
			exts, _ = preferredEncodings(ae)
			if len(exts) == 0 {
				http.Error(w, "not acceptable", http.StatusNotAcceptable)
				return
			}
		}
	}

	for _, ext := range exts {
		if url := s.storage.findURL(ctx, path, ext); url != "" {
			http.Redirect(w, r, url, http.StatusFound)
			return
		}
	}

	http.NotFound(w, r)
}

func (s *server) handleRegistries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	if url := s.storage.findURL(ctx, "registries", ""); url != "" {
		http.Redirect(w, r, url, http.StatusFound)
		return
	}

	http.NotFound(w, r)
}
