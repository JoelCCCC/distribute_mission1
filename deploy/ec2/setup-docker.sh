#!/bin/bash
# ==========================================================
# Automated EC2 Docker & Docker Compose Setup Script
# Supports Ubuntu 22.04/24.04 and Amazon Linux 2023
# ==========================================================

set -e

echo "=== Updating package index and installing prerequisites ==="

if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
fi

if [ "$OS" = "ubuntu" ] || [ "$OS" = "debian" ]; then
    sudo apt-get update -y
    sudo apt-get install -y ca-certificates curl gnupg lsb-release git

    # Install Docker
    sudo install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    sudo chmod a+r /etc/apt/keyrings/docker.gpg

    echo \
      "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
      $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
      sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

    sudo apt-get update -y
    sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

elif [ "$OS" = "amzn" ]; then
    sudo dnf update -y
    sudo dnf install -y docker git
    sudo systemctl enable --now docker
    # Install Docker Compose plugin
    DOCKER_CONFIG=${DOCKER_CONFIG:-/usr/local/lib/docker}
    sudo mkdir -p $DOCKER_CONFIG/cli-plugins
    sudo curl -SL https://github.com/docker/compose/releases/latest/download/docker-compose-linux-$(uname -m) -o $DOCKER_CONFIG/cli-plugins/docker-compose
    sudo chmod +x $DOCKER_CONFIG/cli-plugins/docker-compose
fi

# Allow current user to run docker without sudo
sudo usermod -aG docker "$USER"
sudo systemctl enable docker
sudo systemctl start docker

echo "=== Docker & Docker Compose installation complete! ==="
echo "Note: Log out and log back in, or run 'newgrp docker' to use docker without sudo."
