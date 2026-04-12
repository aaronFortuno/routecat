#!/bin/bash
# RouteCat VPS setup script (Hetzner / Ubuntu 24.04)
# Run as root on a fresh VPS.
set -e

echo "=== RouteCat Gateway Setup ==="

# 1. System user
echo "[1/6] Creating routecat user..."
useradd -r -s /bin/false routecat 2>/dev/null || true
mkdir -p /opt/routecat/data /opt/routecat/lnd
chown -R routecat:routecat /opt/routecat

# 2. Firewall
echo "[2/6] Configuring firewall..."
ufw allow 22/tcp   # SSH
ufw allow 80/tcp   # HTTP (Caddy redirect)
ufw allow 443/tcp  # HTTPS (Caddy)
ufw --force enable

# 3. Install Caddy
echo "[3/6] Installing Caddy..."
apt-get update -qq
apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt-get update -qq
apt-get install -y -qq caddy

# 4. Install Tailscale
echo "[4/6] Installing Tailscale..."
curl -fsSL https://tailscale.com/install.sh | sh
echo ">>> Run 'tailscale up' to authenticate, then note the Umbrel's Tailscale IP"

# 5. Copy config files
echo "[5/6] Installing service files..."
cp Caddyfile /etc/caddy/Caddyfile
cp routecat.service /etc/systemd/system/routecat.service
systemctl daemon-reload

# 6. Instructions
echo "[6/6] Setup complete!"
echo ""
echo "Next steps:"
echo "  1. Build routecat:     GOOS=linux GOARCH=amd64 go build -o routecat ./cmd/routecat"
echo "  2. Upload binary:      scp routecat root@<vps>:/opt/routecat/"
echo "  3. Copy LND creds:     scp admin.macaroon tls.cert root@<vps>:/opt/routecat/lnd/"
echo "  4. Connect Tailscale:  tailscale up (on both VPS and Umbrel)"
echo "  5. Edit service file:  nano /etc/systemd/system/routecat.service"
echo "     → Set -lnd-addr to Umbrel's Tailscale IP:8080"
echo "  6. Point DNS:          route.cat A → <vps-ip>"
echo "  7. Start services:"
echo "     systemctl enable --now caddy"
echo "     systemctl enable --now routecat"
echo "  8. Verify:             curl https://route.cat/v1/models"
echo ""
echo "Security checklist before connecting LND:"
echo "  - [ ] LND macaroon is read-only or invoice+send only (not admin)"
echo "  - [ ] Spending cap set in routecat (-fee and payout engine maxPerHour)"
echo "  - [ ] Tailscale ACL restricts LND port to VPS only"
echo "  - [ ] UFW blocks all except 22/80/443"
echo "  - [ ] routecat runs as unprivileged user"
