BINARY   = storageserver
INSTALL  = /usr/local/bin/$(BINARY)
CONFIG   = /etc/storageserver/config.toml
SERVICE  = /etc/systemd/system/$(BINARY).service
CADDY    = /etc/caddy/Caddyfile
LOGCONF  = /etc/logrotate.d/$(BINARY)
LOGDIR   = /var/log/$(BINARY)

SOURCES = $(shell find . -name '*.go') go.mod go.sum

.PHONY: test install caddy deploy

$(BINARY): $(SOURCES)
	go build -o $(BINARY) .

$(INSTALL): $(BINARY)
	sudo install -m 0755 $(BINARY) $(INSTALL)

# Install config only if not already present, so re-running `make install`
# never silently overwrites the server-side config with whatever happens to
# be in the working tree. Edit /etc/storageserver/config.toml in place to
# update it.
$(CONFIG): config.toml
	sudo mkdir -p /etc/storageserver
	@if [ -f $(CONFIG) ]; then \
		echo "$(CONFIG) already exists; leaving it untouched"; \
	else \
		sudo cp $< $@; \
	fi

# Install Caddy config. Create a Caddyfile based on Caddyfile.example before running.
$(CADDY): Caddyfile
	sudo mkdir -p /etc/caddy
	sudo cp $< $@
	sudo systemctl reload caddy

$(LOGCONF): storageserver.logrotate
	sudo cp $< $@

test:
	go test ./...

# First-time install: create user, install binary and config, install and start service.
# Safe to run more than once.
install: $(INSTALL) $(CONFIG) $(LOGCONF)
	id -u $(BINARY) > /dev/null 2>&1 || sudo useradd -r -s /sbin/nologin $(BINARY)
	sudo mkdir -p $(LOGDIR)
	sudo chown $(BINARY):$(BINARY) $(LOGDIR)
	diff -q storageserver.service $(SERVICE) > /dev/null 2>&1 || sudo cp storageserver.service $(SERVICE)
	sudo systemctl daemon-reload
	sudo systemctl enable --now $(BINARY)

caddy: $(CADDY)

# Install binary and restart the service.
deploy: $(INSTALL)
	sudo systemctl restart $(BINARY)
