#!/usr/bin/env bash
# ==============================================================================
# Script: install-docker-ubuntu.sh
# Purpose: Complete, automated installation of Docker & Docker Compose on Ubuntu
# Supported OS: Ubuntu 20.04 LTS, 22.04 LTS, 24.04 LTS (x86_64 / arm64)
# ==============================================================================

set -euo pipefail

# Text formatting
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}=====================================================${NC}"
echo -e "${BLUE}   Installing Docker & Docker Compose on Ubuntu       ${NC}"
echo -e "${BLUE}=====================================================${NC}"

# 1. Verify OS is Ubuntu
if [ -f /etc/os-release ]; then
    . /etc/os-release
    if [ "$ID" != "ubuntu" ] && [ "$ID_LIKE" != "ubuntu" ]; then
        echo -e "${RED}[ERROR] This script is intended for Ubuntu. Detected OS: $ID${NC}"
        exit 1
    fi
else
    echo -e "${RED}[ERROR] Cannot detect operating system release info.${NC}"
    exit 1
fi

echo -e "${GREEN}[1/6] Removing any outdated or conflicting docker packages...${NC}"
for pkg in docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc; do
    sudo apt-get remove -y "$pkg" 2>/dev/null || true
done

echo -e "${GREEN}[2/6] Updating apt index and installing system dependencies...${NC}"
sudo apt-get update -y
sudo apt-get install -y ca-certificates curl gnupg lsb-release

echo -e "${GREEN}[3/6] Adding Docker's official GPG key...${NC}"
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

echo -e "${GREEN}[4/6] Setting up Docker apt repository...${NC}"
ARCH=$(dpkg --print-architecture)
CODENAME=$(. /etc/os-release && echo "$VERSION_CODENAME")

echo \
  "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
  ${CODENAME} stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt-get update -y

echo -e "${GREEN}[5/6] Installing Docker Engine, CLI, Containerd, and Docker Compose...${NC}"
sudo apt-get install -y \
    docker-ce \
    docker-ce-cli \
    containerd.io \
    docker-buildx-plugin \
    docker-compose-plugin

echo -e "${GREEN}[6/6] Configuring Docker service & user permissions...${NC}"
sudo systemctl enable docker
sudo systemctl start docker

# Add current user to docker group if not already present
CURRENT_USER="${SUDO_USER:-$USER}"
if ! groups "$CURRENT_USER" | grep -q '\bdocker\b'; then
    echo -e "${YELLOW}Adding user '$CURRENT_USER' to the docker group...${NC}"
    sudo usermod -aG docker "$CURRENT_USER"
fi

echo -e "\n${BLUE}=====================================================${NC}"
echo -e "${GREEN}   Docker Installation Successfully Completed!      ${NC}"
echo -e "${BLUE}=====================================================${NC}"

# Verification
docker_version=$(docker --version 2>/dev/null || echo "Docker installed")
compose_version=$(docker compose version 2>/dev/null || echo "Docker Compose installed")

echo -e "  - ${GREEN}${docker_version}${NC}"
echo -e "  - ${GREEN}${compose_version}${NC}"
echo -e "${BLUE}=====================================================${NC}"
echo -e "${YELLOW}[IMPORTANT] To run docker commands without 'sudo':${NC}"
echo -e "Run: ${GREEN}newgrp docker${NC}  (or log out and log back into your EC2 SSH session)"
echo -e "${BLUE}=====================================================${NC}\n"
