BINARY  = storageserver
INSTALL = /usr/local/bin/$(BINARY)
CONFIG  = /etc/storageserver/config.toml
SERVICE = /etc/systemd/system/$(BINARY).service
CADDY   = /etc/caddy/Caddyfile
LOGCONF = /etc/logrotate.d/$(BINARY)
LOGDIR  = /var/log/$(BINARY)

SOURCES = $(shell find . -name '*.go') go.mod go.sum

.PHONY: up test check-prereqs setup-system

# Bring the deployment up to date. Idempotent — safe to re-run.
# Builds and installs the binary, copies config / Caddyfile / logrotate /
# systemd unit when their sources are newer, and reloads/restarts the
# relevant services.
up: check-prereqs setup-system $(INSTALL) $(CONFIG) $(CADDY) $(LOGCONF) $(SERVICE)
	@echo "Up to date."

test:
	go test ./...

check-prereqs:
	@command -v go    >/dev/null 2>&1 || { echo "error: 'go' not in PATH (install from https://go.dev/dl)" >&2; exit 1; }
	@command -v caddy >/dev/null 2>&1 || { echo "error: 'caddy' not in PATH (install per https://caddyserver.com/docs/install)" >&2; exit 1; }

# Create service user and log directory if they don't already exist. Idempotent.
setup-system:
	@id -u $(BINARY) >/dev/null 2>&1 || sudo useradd -r -s /sbin/nologin $(BINARY)
	@sudo mkdir -p $(LOGDIR)
	@sudo chown $(BINARY):$(BINARY) $(LOGDIR)

# Local build artifact.
$(BINARY): $(SOURCES)
	go build -o $(BINARY) .

# Install the binary. install(1) (unlike cp) replaces the file by inode, so
# this works even while the service is running. Restart the service only if
# it's already active — on first install, $(SERVICE) below will start it.
$(INSTALL): $(BINARY)
	sudo install -m 0755 $< $@
	@systemctl is-active --quiet $(BINARY) && sudo systemctl restart $(BINARY) || true

$(CONFIG): config.toml
	sudo mkdir -p $(dir $@)
	sudo cp $< $@
	@systemctl is-active --quiet $(BINARY) && sudo systemctl restart $(BINARY) || true

# Install the systemd unit. daemon-reload picks up the new unit, then
# restart applies it (and starts the service the first time around).
$(SERVICE): storageserver.service
	sudo cp $< $@
	sudo systemctl daemon-reload
	sudo systemctl enable $(BINARY)
	sudo systemctl restart $(BINARY)

$(CADDY): Caddyfile
	sudo mkdir -p $(dir $@)
	sudo cp $< $@
	sudo systemctl reload caddy

$(LOGCONF): storageserver.logrotate
	sudo cp $< $@
