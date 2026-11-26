# Distributed CDN in Go (5-Server AWS EC2 Architecture)

A high-performance, geo-distributed Content Delivery Network (CDN) implemented in **Go (Golang)** with **MongoDB GridFS**, designed to be deployed across **5 AWS EC2 instances**.

---

## 🏛️ Architecture Overview

The system is partitioned into 5 independent server roles:

```
[ Client / Browser ]
        │
        ├─► [ Server 1: Web App Portal & Geo-Router ] (Port 3000)
        │         │
        │         └─► [ Server 2: Central API & MongoDB ] (Port 4000)
        │                   │ (Async Parallel Replication)
        │                   ├──► [ Server 3: Storage Node SG (Asia) ] (Port 5001 + Mongo)
        │                   ├──► [ Server 4: Storage Node NY (NA) ]   (Port 5002 + Mongo)
        │                   └──► [ Server 5: Storage Node LDN (EU) ]  (Port 5003 + Mongo)
        │
        └─► Direct fast downloads stream from the closest Edge Node (sg-1 / ny-1 / ldn-1)
```

| Server Directory | Role | Description | Default Port |
| :--- | :--- | :--- | :--- |
| **`server1-webapp/`** | Server 1 | Frontend SPA & Intelligent Geo-IP Router | `3000` |
| **`server2-central-api/`** | Server 2 | Central API Orchestrator, Master MongoDB & Replication Worker | `4000` + `27017` |
| **`server3-storage-sg/`** | Server 3 | Asia Edge Storage Node (Singapore `sg-1`) + MongoDB GridFS | `5001` + `27018` |
| **`server4-storage-ny/`** | Server 4 | North America Edge Storage Node (New York `ny-1`) + MongoDB GridFS | `5002` + `27019` |
| **`server5-storage-ldn/`** | Server 5 | Europe Edge Storage Node (London `ldn-1`) + MongoDB GridFS | `5003` + `27020` |

---

## 📂 MongoDB Document Schema

Every file uploaded to Server 2 is stored in Central MongoDB (`cdn_central.files`) with the exact schema format:

```json
{
  "file_id": "img_98765",
  "filename": "banner.png",
  "size_bytes": 245120,
  "url_forward": "http://198.45.98.44:5001/files/img_98765/banner.png",
  "replicas": [
    {
      "node_id": "sg-1",
      "region": "Asia",
      "url": "http://198.45.98.44:5001/files/img_98765/banner.png",
      "status": "ready"
    },
    {
      "node_id": "ny-1",
      "region": "North America",
      "url": "http://54.210.12.33:5002/files/img_98765/banner.png",
      "status": "ready"
    },
    {
      "node_id": "ldn-1",
      "region": "Europe",
      "url": "http://18.130.45.67:5003/files/img_98765/banner.png",
      "status": "ready"
    }
  ]
}
```

---

## 🚀 Quickstart: Local 5-Server Multi-Node Testing

You can spin up all 5 servers with their respective MongoDB instances locally on your machine with a single command:

```bash
# 1. Start all 5 servers + 4 MongoDB containers
make run-all
# Or: docker compose up -d --build

# 2. Open Server 1 (Webapp) in your browser:
open http://localhost:3000
```

### Local Endpoints
- **Server 1 (Web Portal)**: [http://localhost:3000](http://localhost:3000)
- **Server 2 (Central API)**: [http://localhost:4000](http://localhost:4000)
- **Server 3 (Singapore Edge)**: [http://localhost:5001](http://localhost:5001)
- **Server 4 (New York Edge)**: [http://localhost:5002](http://localhost:5002)
- **Server 5 (London Edge)**: [http://localhost:5003](http://localhost:5003)

---

## ☁️ AWS EC2 Deployment Guide

### Prerequisites on AWS
1. Launch **5 EC2 instances** (e.g. Ubuntu 22.04 or Amazon Linux 2023, `t3.small` or `t3.medium`).
2. Configure **Security Groups**:
   - **EC2 #1 (Webapp)**: Inbound TCP `3000` (or `80`/`443`) from `0.0.0.0/0`.
   - **EC2 #2 (Central API)**: Inbound TCP `4000` from `0.0.0.0/0` (or restricted to EC2 #1 and internal).
   - **EC2 #3, #4, #5 (Storage Nodes)**: Inbound TCP `5001`, `5002`, `5003` from `0.0.0.0/0`.
3. Note down the public IP of each EC2 instance:
   - `SERVER_1_IP`
   - `SERVER_2_IP`
   - `SERVER_3_IP` (Singapore `sg-1`)
   - `SERVER_4_IP` (New York `ny-1`)
   - `SERVER_5_IP` (London `ldn-1`)

---

### Step 1: Install Docker on all EC2 instances
Run the automated setup script on each instance:
```bash
git clone https://github.com/<your-username>/mission1_cdn.git
cd mission1_cdn
./deploy/ec2/setup-docker.sh
newgrp docker
```

---

### Step 2: Deploy Server 3, 4, 5 (Edge Storage Nodes)

#### On EC2 Instance #3 (Singapore `sg-1`):
```bash
cd mission1_cdn/server3-storage-sg
cp .env.example .env

# Edit .env and set your EC2 #3 public IP:
# PUBLIC_URL=http://<SERVER_3_IP>:5001

docker compose up -d --build
```

#### On EC2 Instance #4 (New York `ny-1`):
```bash
cd mission1_cdn/server4-storage-ny
cp .env.example .env

# Edit .env and set your EC2 #4 public IP:
# PUBLIC_URL=http://<SERVER_4_IP>:5002

docker compose up -d --build
```

#### On EC2 Instance #5 (London `ldn-1`):
```bash
cd mission1_cdn/server5-storage-ldn
cp .env.example .env

# Edit .env and set your EC2 #5 public IP:
# PUBLIC_URL=http://<SERVER_5_IP>:5003

docker compose up -d --build
```

---

### Step 3: Deploy Server 2 (Central API)

#### On EC2 Instance #2:
```bash
cd mission1_cdn/server2-central-api
cp .env.example .env

# Edit .env and supply the Storage Node IPs:
# EDGE_SG_HOST=<SERVER_3_IP>:5001
# EDGE_SG_URL=http://<SERVER_3_IP>:5001
# EDGE_NY_HOST=<SERVER_4_IP>:5002
# EDGE_NY_URL=http://<SERVER_4_IP>:5002
# EDGE_LDN_HOST=<SERVER_5_IP>:5003
# EDGE_LDN_URL=http://<SERVER_5_IP>:5003

docker compose up -d --build
```

---

### Step 4: Deploy Server 1 (Webapp Frontend)

#### On EC2 Instance #1:
```bash
cd mission1_cdn/server1-webapp
cp .env.example .env

# Edit .env and point to Server 2 Central API:
# CENTRAL_API_URL=http://<SERVER_2_IP>:4000

docker compose up -d --build
```

---

## 🧪 Verification & Testing

### 1. Run Automated Unit & Integration Tests
```bash
make test
```

### 2. Manual End-to-End Verification
1. Open `http://<SERVER_1_IP>:3000` in your web browser.
2. Drag and drop any image or file (e.g. `banner.png`) and click **"Upload & Replicate"**.
3. **Observe Asynchronous Replication**:
   - The file appears immediately with replica badges in `syncing` (⏳) state.
   - Within 1-2 seconds, the background workers on Server 2 push the chunks into MongoDB GridFS across Singapore, New York, and London, and the badges turn to `ready` (✅).
4. **Test Geo-Routing**:
   - In the top navigation bar, use the **"Simulate Region"** dropdown to switch between:
     - `Asia` ➔ Click **"⚡ Nearest Route"** ➔ Browser is 302 redirected directly to Server 3 (`sg-1`).
     - `North America` ➔ Click **"⚡ Nearest Route"** ➔ Browser is 302 redirected directly to Server 4 (`ny-1`).
     - `Europe` ➔ Click **"⚡ Nearest Route"** ➔ Browser is 302 redirected directly to Server 5 (`ldn-1`).
5. **Inspect MongoDB Document Schema**:
   - Click the **"📄 JSON"** button in the file row to view the exact JSON document stored in Server 2's MongoDB, complete with `url_forward` and the `replicas` array.
