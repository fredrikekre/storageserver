# storageserver

A minimal HTTP server for Julia package storage. Clients send a `GET` request; the server
checks one or more S3-compatible backends in order and returns a `302` redirect to the first
URL that exists.

TLS is handled by [Caddy](https://caddyserver.com), which runs in front and proxies to this
server on `localhost:8080`.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET/HEAD | `/registry/<uuid>/<sha>` | Registry tarball |
| GET/HEAD | `/package/<uuid>/<sha>` | Package tarball |
| GET/HEAD | `/artifact/<sha>` | Artifact tarball |
| GET/HEAD | `/registries` | List of known registries |

Paths may include an explicit compression extension (`.tar.gz`, `.gz`, `.tar.zst`, `.zst`).
Without one, the server negotiates via `Accept-Encoding` (gzip default; zstd if requested).

## Configuration

```toml
server_addr = "127.0.0.1:8080"

# Backends are checked in order; first match wins.
# Use the full base URL — works for any S3-compatible storage.

[[storage_backends]]
url = "https://pub-abc123.r2.dev"   # Cloudflare R2 public bucket

[[storage_backends]]
url = "https://julialang-storage-us-east-1.s3.us-east-1.amazonaws.com"
```

Copy `config.toml.example` as a starting point.

## Building and deploying

Requires Go 1.21+ on your build machine. The server itself needs no runtime — the binary is
statically linked.

**Build for the target server** (cross-compile if needed):

```sh
GOOS=linux GOARCH=amd64 make storageserver   # Intel/AMD
GOOS=linux GOARCH=arm64 make storageserver   # ARM
```

**First-time install** (run on the server):

```sh
cp config.toml.example config.toml   # then edit config.toml
cp Caddyfile.example Caddyfile       # then edit Caddyfile
make install                         # creates user, installs binary, config, and service
make caddy                           # installs Caddy config
```

**Deploy** (subsequent updates):

```sh
make deploy   # installs updated binary and restarts service
```

**Run tests:**

```sh
make test
```

## TLS with Caddy

Install Caddy, then run `make caddy` as described above. Caddy obtains and renews
Let's Encrypt certificates automatically and redirects HTTP to HTTPS.

See `Caddyfile.example` for configuration options.
