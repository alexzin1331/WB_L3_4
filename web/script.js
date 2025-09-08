// web/script.js
document.getElementById('uploadForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const formData = new FormData(e.target);
    const res = await fetch('/upload', { method: 'POST', body: formData });
    if (res.ok) {
        const { id } = await res.json();
        addImage(id);
    } else {
        alert('Upload failed');
    }
});

function addImage(id) {
    const div = document.createElement('div');
    div.id = `img-${id}`;
    div.innerHTML = `
        <p>ID: ${id} - Status: <span class="status">pending</span></p>
        <img src="/web/loading.gif" alt="processing" class="preview">
        <button onclick="deleteImage('${id}')">Delete</button>
    `;
    document.getElementById('images').appendChild(div);
    pollStatus(id);
}

async function pollStatus(id) {
    const res = await fetch(`/image/${id}`);
    if (res.status === 202) {
        setTimeout(() => pollStatus(id), 2000);
    } else if (res.ok) {
        const blob = await res.blob();
        const url = URL.createObjectURL(blob);
        const status = document.querySelector(`#img-${id} .status`);
        status.textContent = 'done';
        const img = document.querySelector(`#img-${id} .preview`);
        img.src = url;
        img.alt = 'processed image';
    } else {
        alert('Error getting image');
    }
}

async function deleteImage(id) {
    const res = await fetch(`/image/${id}`, { method: 'DELETE' });
    if (res.ok) {
        document.getElementById(`img-${id}`).remove();
    } else {
        alert('Delete failed');
    }
}