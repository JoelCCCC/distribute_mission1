// Distributed Go CDN Client Application

let selectedFile = null;
let currentFiles = [];
let simulatedRegion = '';
let detectedRegion = 'Asia';
let pollInterval = null;

document.addEventListener('DOMContentLoaded', () => {
  initGeoDetection();
  initUploadDropzone();
  initEventListeners();
  loadFiles();
});

// 1. Geo-Location & Simulation
async function initGeoDetection() {
  try {
    const res = await fetch('/api/geo');
    if (res.ok) {
      const data = await res.json();
      detectedRegion = data.region || 'Asia';
      updateGeoUI(data.region, data.client_ip);
    }
  } catch (err) {
    console.error('Geo detection error:', err);
    updateGeoUI('Asia', '127.0.0.1');
  }
}

function updateGeoUI(region, ip) {
  const activeRegion = simulatedRegion || region;
  document.getElementById('detected-region').textContent = activeRegion;
  document.getElementById('client-ip').textContent = `(IP: ${ip || '--'})`;

  // Highlight corresponding topology card
  document.querySelectorAll('.topo-card.edge').forEach(card => card.classList.remove('highlighted'));
  if (activeRegion.includes('Asia')) {
    document.getElementById('card-sg')?.classList.add('highlighted');
  } else if (activeRegion.includes('North America') || activeRegion.includes('America')) {
    document.getElementById('card-ny')?.classList.add('highlighted');
  } else if (activeRegion.includes('Europe')) {
    document.getElementById('card-ldn')?.classList.add('highlighted');
  }
}

// 2. Drag & Drop File Upload
function initUploadDropzone() {
  const dropzone = document.getElementById('dropzone');
  const fileInput = document.getElementById('file-input');
  const browseBtn = document.getElementById('browse-btn');

  browseBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    fileInput.click();
  });

  dropzone.addEventListener('click', () => fileInput.click());

  dropzone.addEventListener('dragover', (e) => {
    e.preventDefault();
    dropzone.classList.add('dragover');
  });

  dropzone.addEventListener('dragleave', () => dropzone.classList.remove('dragover'));

  dropzone.addEventListener('drop', (e) => {
    e.preventDefault();
    dropzone.classList.remove('dragover');
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      handleFileSelected(e.dataTransfer.files[0]);
    }
  });

  fileInput.addEventListener('change', () => {
    if (fileInput.files && fileInput.files.length > 0) {
      handleFileSelected(fileInput.files[0]);
    }
  });
}

function handleFileSelected(file) {
  selectedFile = file;
  document.getElementById('preview-filename').textContent = file.name;
  document.getElementById('preview-size').textContent = formatBytes(file.size);
  document.getElementById('preview-bar').style.display = 'flex';
}

function initEventListeners() {
  document.getElementById('start-upload-btn').addEventListener('click', uploadSelectedFile);
  document.getElementById('refresh-btn').addEventListener('click', loadFiles);

  const simulateSelect = document.getElementById('simulate-region');
  simulateSelect.addEventListener('change', (e) => {
    simulatedRegion = e.target.value;
    updateGeoUI(simulatedRegion || detectedRegion, 'Simulated Client');
  });

  // Modal events
  document.getElementById('close-modal-btn').addEventListener('click', closeModal);
  document.getElementById('close-modal-footer-btn').addEventListener('click', closeModal);
  document.getElementById('copy-json-btn').addEventListener('click', copyJSON);
}

// 3. Upload File to Central API
async function uploadSelectedFile() {
  if (!selectedFile) return;

  const btn = document.getElementById('start-upload-btn');
  btn.disabled = true;

  const progressContainer = document.getElementById('progress-container');
  const progressBar = document.getElementById('progress-bar');
  const progressText = document.getElementById('progress-text');

  progressContainer.style.display = 'block';
  progressBar.style.width = '30%';
  progressText.textContent = 'Uploading file to Central API (Server 2)...';

  const formData = new FormData();
  formData.append('file', selectedFile);

  try {
    const res = await fetch('/api/upload', {
      method: 'POST',
      body: formData,
    });

    if (!res.ok) {
      const errText = await res.text();
      throw new Error(`Upload failed: ${errText}`);
    }

    const fileDoc = await res.json();
    progressBar.style.width = '100%';
    progressText.textContent = 'Uploaded! Server 2 is replicating to SG, NY, and LDN nodes...';

    // Reset preview
    setTimeout(() => {
      progressContainer.style.display = 'none';
      document.getElementById('preview-bar').style.display = 'none';
      selectedFile = null;
      btn.disabled = false;
      document.getElementById('file-input').value = '';
    }, 1200);

    // Refresh file list and trigger active polling for replication completion
    await loadFiles();
    startReplicationPolling();
  } catch (err) {
    alert(err.message);
    progressContainer.style.display = 'none';
    btn.disabled = false;
  }
}

// 4. Load Files & Render Table
async function loadFiles() {
  try {
    const res = await fetch('/api/files');
    if (!res.ok) throw new Error('Failed to fetch files');
    currentFiles = await res.json();
    renderFilesTable(currentFiles);

    // Check if any file is still syncing
    const hasSyncing = currentFiles.some(f => f.replicas && f.replicas.some(r => r.status === 'syncing'));
    if (hasSyncing) {
      startReplicationPolling();
    } else {
      stopReplicationPolling();
    }
  } catch (err) {
    console.error('Error fetching files:', err);
    document.getElementById('files-tbody').innerHTML = `
      <tr><td colspan="5" class="empty-state">Unable to load files from Central API (Server 2)</td></tr>
    `;
  }
}

function startReplicationPolling() {
  if (pollInterval) return;
  pollInterval = setInterval(async () => {
    const res = await fetch('/api/files');
    if (res.ok) {
      currentFiles = await res.json();
      renderFilesTable(currentFiles);
      const hasSyncing = currentFiles.some(f => f.replicas && f.replicas.some(r => r.status === 'syncing'));
      if (!hasSyncing) {
        stopReplicationPolling();
      }
    }
  }, 2000);
}

function stopReplicationPolling() {
  if (pollInterval) {
    clearInterval(pollInterval);
    pollInterval = null;
  }
}

function renderFilesTable(files) {
  const tbody = document.getElementById('files-tbody');
  if (!files || files.length === 0) {
    tbody.innerHTML = `<tr><td colspan="5" class="empty-state">No files uploaded yet. Upload a banner or media above!</td></tr>`;
    return;
  }

  tbody.innerHTML = files.map(file => {
    const replicasHTML = (file.replicas || []).map(r => {
      const statusIcon = r.status === 'ready' ? '✅' : (r.status === 'syncing' ? '⏳' : '❌');
      return `
        <a href="${r.url}" target="_blank" class="replica-chip ${r.status}" title="Node: ${r.node_id} (${r.region})\nURL: ${r.url}">
          <span>${statusIcon} ${r.node_id}</span>
          <small>(${r.region})</small>
        </a>
      `;
    }).join('');

    const regionParam = simulatedRegion ? `?region=${encodeURIComponent(simulatedRegion)}` : '';
    const routedCDNLink = `/cdn/${file.file_id}/${file.filename}${regionParam}`;

    return `
      <tr>
        <td>
          <div class="file-cell">
            <span class="file-cell-name">${escapeHTML(file.filename)}</span>
            <span class="file-cell-id">${file.file_id}</span>
          </div>
        </td>
        <td>${formatBytes(file.size_bytes)}</td>
        <td>
          <div class="url-forward-box">
            <a href="${file.url_forward}" target="_blank" class="url-forward-text" title="${file.url_forward}">
              ${file.url_forward}
            </a>
          </div>
        </td>
        <td>
          <div class="replicas-list">
            ${replicasHTML}
          </div>
        </td>
        <td>
          <div class="actions-cell">
            <a href="${routedCDNLink}" target="_blank" class="btn btn-primary btn-sm" title="Smart Geo-Route to closest storage node">
              ⚡ Nearest Route
            </a>
            <button class="btn btn-secondary btn-sm" onclick="showFileJSON('${file.file_id}')">
              📄 JSON
            </button>
            <button class="btn btn-danger btn-sm" onclick="deleteFile('${file.file_id}')">
              🗑️
            </button>
          </div>
        </td>
      </tr>
    `;
  }).join('');
}

// 5. Modal Inspection
window.showFileJSON = function(fileID) {
  const file = currentFiles.find(f => f.file_id === fileID);
  if (!file) return;

  // Format exactly matching user's requested specification
  const formattedJSON = {
    file_id: file.file_id,
    filename: file.filename,
    size_bytes: file.size_bytes,
    url_forward: file.url_forward,
    replicas: (file.replicas || []).map(r => ({
      node_id: r.node_id,
      region: r.region,
      url: r.url,
      status: r.status,
    }))
  };

  document.getElementById('json-code').textContent = JSON.stringify(formattedJSON, null, 2);
  document.getElementById('json-modal').style.display = 'flex';
};

function closeModal() {
  document.getElementById('json-modal').style.display = 'none';
}

function copyJSON() {
  const code = document.getElementById('json-code').textContent;
  navigator.clipboard.writeText(code).then(() => {
    const btn = document.getElementById('copy-json-btn');
    const orig = btn.textContent;
    btn.textContent = '✅ Copied!';
    setTimeout(() => btn.textContent = orig, 1500);
  });
}

// 6. Delete File
window.deleteFile = async function(fileID) {
  if (!confirm(`Are you sure you want to delete file ${fileID} from Central API and all edge nodes?`)) return;

  try {
    const res = await fetch(`/api/files/${fileID}`, { method: 'DELETE' });
    if (!res.ok) throw new Error('Failed to delete file');
    await loadFiles();
  } catch (err) {
    alert(err.message);
  }
};

// Utilities
function formatBytes(bytes, decimals = 1) {
  if (!bytes || bytes === 0) return '0 B';
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

function escapeHTML(str) {
  return str.replace(/[&<>'"]/g, tag => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    "'": '&#39;',
    '"': '&quot;'
  }[tag] || tag));
}
