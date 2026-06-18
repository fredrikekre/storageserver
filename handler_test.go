package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testUUID = "00000000-0000-0000-0000-000000000000"
	testHash = "da39a3ee5e6b4b0d3255bfef95601890afd80709"
)

// makeServer returns a server whose backends point at a single fake HTTP server.
// existingKeys lists URL paths the fake server returns 200 HEAD for.
func makeServer(t *testing.T, existingKeys []string) *server {
	t.Helper()
	keySet := make(map[string]bool, len(existingKeys))
	for _, k := range existingKeys {
		keySet[k] = true
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if keySet[r.URL.Path] {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fake.Close)

	cfg := &Config{
		ServerAddr:      "0.0.0.0:8080",
		StorageBackends: []Backend{{URL: fake.URL}},
	}
	st, err := newStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &server{cfg: cfg, storage: st}
}

func get(t *testing.T, h http.Handler, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// --- clientAddr ---

func TestClientAddr(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
		{"no XFF -> RemoteAddr", "", "192.0.2.1:1234", "192.0.2.1:1234"},
		{"single XFF entry", "203.0.113.5", "192.0.2.1:1234", "203.0.113.5"},
		{"chained XFF picks first", "203.0.113.5, 198.51.100.7", "192.0.2.1:1234", "203.0.113.5"},
		{"chained XFF without spaces", "203.0.113.5,198.51.100.7", "192.0.2.1:1234", "203.0.113.5"},
		{"XFF with surrounding spaces", "  203.0.113.5  ", "192.0.2.1:1234", "203.0.113.5"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remoteAddr
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientAddr(req); got != c.want {
			t.Errorf("%s: clientAddr = %q, want %q", c.name, got, c.want)
		}
	}
}

// --- parseResourcePath ---

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		path string
		ext  string
		ok   bool
	}{
		// valid
		{"/registry/" + testUUID + "/" + testHash, "registry/" + testUUID + "/" + testHash, "", true},
		{"/package/" + testUUID + "/" + testHash, "package/" + testUUID + "/" + testHash, "", true},
		{"/artifact/" + testHash, "artifact/" + testHash, "", true},
		{"/artifact/" + testHash + ".tar.gz", "artifact/" + testHash, ".tar.gz", true},
		{"/artifact/" + testHash + ".gz", "artifact/" + testHash, ".tar.gz", true},
		{"/artifact/" + testHash + ".tar.zst", "artifact/" + testHash, ".tar.zst", true},
		{"/artifact/" + testHash + ".zst", "artifact/" + testHash, ".tar.zst", true},
		// wrong segment count
		{"/registry/" + testUUID + "/" + testHash + "/extra", "", "", false},
		{"/registry/" + testUUID, "", "", false},
		{"/artifact/" + testHash + "/extra", "", "", false},
		{"/artifact", "", "", false},
		// unknown prefix
		{"/other/thing", "", "", false},
		{"/", "", "", false},
		// trailing slash
		{"/artifact/" + testHash + "/", "", "", false},
		{"/registry/" + testUUID + "/" + testHash + "/", "", "", false},
		{"/package/" + testUUID + "/" + testHash + "/", "", "", false},
		// invalid format
		{"/artifact/notahex", "", "", false},
		{"/registry/not-a-uuid/" + testHash, "", "", false},
		{"/registry/" + testUUID + "/notahex", "", "", false},
		{"/package/not-a-uuid/" + testHash, "", "", false},
		// UUID must be hex, not arbitrary letters
		{"/registry/gggggggg-gggg-gggg-gggg-gggggggggggg/" + testHash, "", "", false},
	}
	for _, c := range cases {
		gotPath, gotExt, ok := parseResourcePath(c.in)
		if ok != c.ok || gotPath != c.path || gotExt != c.ext {
			t.Errorf("parseResourcePath(%q) = %q, %q, %v; want %q, %q, %v",
				c.in, gotPath, gotExt, ok, c.path, c.ext, c.ok)
		}
	}
}

// --- Accept-Encoding parsing ---

func TestEncodingQ(t *testing.T) {
	cases := []struct {
		header, enc string
		want        float64
	}{
		{"", "zstd", 1.0},
		{"", "gzip", 1.0},
		{"zstd", "zstd", 1.0},
		{"zstd", "gzip", -1},
		{"gzip", "zstd", -1},
		{"zstd;q=0", "zstd", 0},
		{"zstd;q=0.5", "zstd", 0.5},
		{"gzip, zstd;q=0", "zstd", 0},
		{"gzip;q=0, zstd", "gzip", 0},
		{"*", "zstd", 1.0},
		{"*", "gzip", 1.0},
		{"*;q=0", "zstd", 0},
		{"gzip, *;q=0", "zstd", 0},
		{"gzip, *;q=0", "gzip", 1.0},
	}
	for _, c := range cases {
		got := encodingQ(c.header, c.enc)
		if got != c.want {
			t.Errorf("encodingQ(%q, %q) = %v; want %v", c.header, c.enc, got, c.want)
		}
	}
}

func TestNoAcceptEncodingDefaultsToGzip(t *testing.T) {
	srv := makeServer(t, []string{"/artifact/" + testHash + ".tar.gz"})
	rr := get(t, srv.routes(), "/artifact/"+testHash, "")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasSuffix(loc, ".tar.gz") {
		t.Errorf("Location %q should point to .tar.gz", loc)
	}
}

func TestNotAcceptableWhenBothRejected(t *testing.T) {
	srv := makeServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/artifact/"+testHash, nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0, zstd;q=0")
	rr := httptest.NewRecorder()
	srv.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotAcceptable {
		t.Errorf("got %d, want 406", rr.Code)
	}
}

// --- method enforcement ---

func TestMethodNotAllowed(t *testing.T) {
	srv := makeServer(t, nil)
	h := srv.routes()
	for _, path := range []string{
		"/registry/" + testUUID + "/" + testHash,
		"/package/" + testUUID + "/" + testHash,
		"/artifact/" + testHash,
		"/registries",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: got %d, want 405", path, rr.Code)
		}
	}
}

// --- 404 on bad paths ---

func TestBadPathsReturn404(t *testing.T) {
	srv := makeServer(t, nil)
	h := srv.routes()
	for _, path := range []string{
		"/registry/" + testUUID,
		"/registry/" + testUUID + "/" + testHash + "/extra",
		"/artifact/" + testHash + "/extra",
		"/package/" + testUUID,
		"/artifact/notahex",
		"/registry/not-a-uuid/" + testHash,
	} {
		rr := get(t, h, path, "")
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", path, rr.Code)
		}
	}
}

// --- redirect ---

func TestRedirectPrefersZstd(t *testing.T) {
	srv := makeServer(t, []string{"/artifact/" + testHash + ".tar.zst"})
	rr := get(t, srv.routes(), "/artifact/"+testHash, "zstd")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasSuffix(loc, ".tar.zst") {
		t.Errorf("Location %q should point to .tar.zst", loc)
	}
}

func TestRedirectFallsBackToGzip(t *testing.T) {
	srv := makeServer(t, []string{"/artifact/" + testHash + ".tar.gz"})
	rr := get(t, srv.routes(), "/artifact/"+testHash, "zstd, gzip")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasSuffix(loc, ".tar.gz") {
		t.Errorf("Location %q should point to .tar.gz", loc)
	}
}

func TestNeitherKeyReturns404(t *testing.T) {
	srv := makeServer(t, nil)
	rr := get(t, srv.routes(), "/artifact/"+testHash, "zstd")
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

func TestMultipleBackendsFallThrough(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fallback.Close)

	cfg := &Config{
		StorageBackends: []Backend{
			{URL: primary.URL},
			{URL: fallback.URL},
		},
	}
	st, err := newStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{cfg: cfg, storage: st}

	rr := get(t, srv.routes(), "/artifact/"+testHash, "zstd")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, fallback.URL) {
		t.Errorf("Location %q should point to fallback %s", loc, fallback.URL)
	}
}

func TestFirstBackendErrorFallsThrough(t *testing.T) {
	// Primary is closed before requests arrive, so connections are refused.
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primaryURL := primary.URL
	primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fallback.Close)

	cfg := &Config{
		StorageBackends: []Backend{
			{URL: primaryURL},
			{URL: fallback.URL},
		},
	}
	st, err := newStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{cfg: cfg, storage: st}

	rr := get(t, srv.routes(), "/artifact/"+testHash, "zstd")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, fallback.URL) {
		t.Errorf("Location %q should point to fallback %s", loc, fallback.URL)
	}
}

func TestURLExtensionOverridesAcceptEncoding(t *testing.T) {
	srv := makeServer(t, []string{"/artifact/" + testHash + ".tar.gz"})
	rr := get(t, srv.routes(), "/artifact/"+testHash+".tar.gz", "zstd")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasSuffix(loc, ".tar.gz") {
		t.Errorf("Location %q should point to .tar.gz", loc)
	}
}

func TestURLExtensionAliasesNormalized(t *testing.T) {
	srv := makeServer(t, []string{"/artifact/" + testHash + ".tar.zst"})
	for _, alias := range []string{
		"/artifact/" + testHash + ".tar.zst",
		"/artifact/" + testHash + ".zst",
	} {
		rr := get(t, srv.routes(), alias, "")
		if rr.Code != http.StatusFound {
			t.Errorf("GET %s: got %d, want 302", alias, rr.Code)
		}
	}
}

// --- /registries ---

func TestRegistriesRedirects(t *testing.T) {
	srv := makeServer(t, []string{"/registries"})
	rr := get(t, srv.routes(), "/registries", "")
	if rr.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasSuffix(loc, "/registries") {
		t.Errorf("Location %q should point to /registries", loc)
	}
}

func TestRegistriesMissingReturns404(t *testing.T) {
	srv := makeServer(t, nil)
	rr := get(t, srv.routes(), "/registries", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}
