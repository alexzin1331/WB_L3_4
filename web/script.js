// Global state
let uploadedImages = new Map();
let pollingIntervals = new Map();

// DOM elements
const uploadForm = document.getElementById('uploadForm');
const imageInput = document.getElementById('imageInput');
const imagesContainer = document.getElementById('images');
const noImagesMessage = document.getElementById('no-images');
const fileInputLabel = document.querySelector('.file-input-label .upload-text');
const uploadBtn = document.querySelector('.upload-btn');
const modal = document.getElementById('imageModal');
const modalImage = document.getElementById('modalImage');
const closeModal = document.querySelector('.close');

// Initialize
document.addEventListener('DOMContentLoaded', function() {
    setupEventListeners();
    updateNoImagesMessage();
});

function setupEventListeners() {
    // Upload form
    uploadForm.addEventListener('submit', handleUpload);
    
    // File input change
    imageInput.addEventListener('change', function() {
        const fileName = this.files[0]?.name || 'Choose Image File';
        fileInputLabel.textContent = fileName;
    });
    
    // Modal
    closeModal.addEventListener('click', closeImageModal);
    modal.addEventListener('click', function(e) {
        if (e.target === modal) {
            closeImageModal();
        }
    });
    
    // Keyboard navigation
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') {
            closeImageModal();
        }
    });
}

async function handleUpload(e) {
    e.preventDefault();
    
    const formData = new FormData(e.target);
    const file = imageInput.files[0];
    
    if (!file) {
        showNotification('Please select an image file', 'error');
        return;
    }
    
    // Validate file size (10MB)
    if (file.size > 10 * 1024 * 1024) {
        showNotification('File too large. Maximum size is 10MB', 'error');
        return;
    }
    
    // Validate file type
    if (!file.type.startsWith('image/')) {
        showNotification('Please select a valid image file', 'error');
        return;
    }
    
    // Disable upload button
    uploadBtn.disabled = true;
    uploadBtn.innerHTML = '<span class="btn-icon">⏳</span> Uploading...';
    
    try {
        const response = await fetch('/upload', {
            method: 'POST',
            body: formData
        });
        
        const result = await response.json();
        
        if (response.ok) {
            showNotification('Image uploaded successfully!', 'success');
            addImageToGrid(result.id, file);
            
            // Reset form
            uploadForm.reset();
            fileInputLabel.textContent = 'Choose Image File';
        } else {
            showNotification(result.error || 'Upload failed', 'error');
        }
    } catch (error) {
        console.error('Upload error:', error);
        showNotification('Upload failed. Please try again.', 'error');
    } finally {
        // Re-enable upload button
        uploadBtn.disabled = false;
        uploadBtn.innerHTML = '<span class="btn-icon">⬆️</span> Upload & Process';
    }
}

function addImageToGrid(imageId, file) {
    // Create image card
    const imageCard = document.createElement('div');
    imageCard.className = 'image-card';
    imageCard.id = `img-${imageId}`;
    
    // Create preview from file for original
    const fileReader = new FileReader();
    fileReader.onload = function(e) {
        const originalPreview = imageCard.querySelector('.original-preview');
        if (originalPreview) {
            originalPreview.src = e.target.result;
        }
    };
    fileReader.readAsDataURL(file);
    
    imageCard.innerHTML = `
        <div class="image-info">
            <div class="image-id">ID: ${imageId}</div>
            <span class="status pending">pending</span>
        </div>
        
        <div class="image-gallery">
            <div class="image-item">
                <h4>📷 Original</h4>
                <img class="image-preview original-preview" src="" alt="Original image" onclick="openImageModal('${imageId}', 'original')">
            </div>
            <div class="image-item">
                <h4>🔄 Resized</h4>
                <img class="image-preview resized-preview" src="" alt="Resized image" onclick="openImageModal('${imageId}', 'resized')">
                <div class="processing-status">
                    <span class="process-status resize-status pending">pending</span>
                </div>
            </div>
            <div class="image-item">
                <h4>🖼️ Thumbnail</h4>
                <img class="image-preview thumbnail-preview" src="" alt="Thumbnail" onclick="openImageModal('${imageId}', 'thumbnail')">
                <div class="processing-status">
                    <span class="process-status thumbnail-status pending">pending</span>
                </div>
            </div>
            <div class="image-item">
                <h4>💧 Watermarked</h4>
                <img class="image-preview watermarked-preview" src="" alt="Watermarked image" onclick="openImageModal('${imageId}', 'watermarked')">
                <div class="processing-status">
                    <span class="process-status watermark-status pending">pending</span>
                </div>
            </div>
        </div>
        
        <div class="image-actions">
            <button class="btn btn-danger" onclick="deleteImage('${imageId}')">🗑️ Delete</button>
            <button class="btn btn-info" onclick="viewImageInfo('${imageId}')">ℹ️ Info</button>
            <button class="btn btn-secondary" onclick="downloadAll('${imageId}')">💾 Download All</button>
        </div>
        <div class="processing-actions">
            <button class="btn btn-process" onclick="triggerResize('${imageId}')">🔄 Resize</button>
            <button class="btn btn-process" onclick="triggerThumbnail('${imageId}')">🖼️ Thumbnail</button>
            <button class="btn btn-process" onclick="triggerWatermark('${imageId}')">💧 Watermark</button>
        </div>
    `;
    
    imagesContainer.appendChild(imageCard);
    uploadedImages.set(imageId, {
        status: 'pending',
        originalFile: file
    });
    
    updateNoImagesMessage();
    startPolling(imageId);
}

function startPolling(imageId) {
    // Clear existing interval if any
    if (pollingIntervals.has(imageId)) {
        clearInterval(pollingIntervals.get(imageId));
    }
    
    const pollInterval = setInterval(async () => {
        try {
            // Get detailed info about all processing statuses
            const infoResponse = await fetch(`/image/${imageId}/info`);
            
            if (infoResponse.ok) {
                const info = await infoResponse.json();
                
                // Update overall status
                updateImageStatus(imageId, info.status);
                
                // Update individual processing statuses
                updateProcessingStatus(imageId, 'resize', info.resize_status);
                updateProcessingStatus(imageId, 'thumbnail', info.thumbnail_status);
                updateProcessingStatus(imageId, 'watermark', info.watermark_status);
                
                // Load processed images if they're done
                await loadProcessedImages(imageId, info);
                
                // Stop polling if all processing is complete
                if (info.status === 'done' || info.status === 'error') {
                    clearInterval(pollInterval);
                    pollingIntervals.delete(imageId);
                }
            }
        } catch (error) {
            console.error('Polling error:', error);
            // Don't stop polling on network errors, just log them
        }
    }, 2000);
    
    pollingIntervals.set(imageId, pollInterval);
}

function updateImageStatus(imageId, status) {
    const statusElement = document.querySelector(`#img-${imageId} .status`);
    if (statusElement) {
        statusElement.textContent = status;
        statusElement.className = `status ${status}`;
    }
    
    const imageData = uploadedImages.get(imageId);
    if (imageData) {
        imageData.status = status;
        uploadedImages.set(imageId, imageData);
    }
}

function updateProcessingStatus(imageId, processType, status) {
    const statusElement = document.querySelector(`#img-${imageId} .${processType}-status`);
    if (statusElement) {
        statusElement.textContent = status;
        statusElement.className = `process-status ${processType}-status ${status}`;
    }
}

async function loadProcessedImages(imageId, info) {
    // Load resized image
    if (info.resize_status === 'done') {
        try {
            const response = await fetch(`/image/${imageId}`);
            if (response.ok) {
                const blob = await response.blob();
                const url = URL.createObjectURL(blob);
                const resizedImg = document.querySelector(`#img-${imageId} .resized-preview`);
                if (resizedImg) {
                    resizedImg.src = url;
                    resizedImg.alt = 'Resized image';
                }
            }
        } catch (error) {
            console.error('Error loading resized image:', error);
        }
    }
    
    // Load thumbnail
    if (info.thumbnail_status === 'done') {
        try {
            const response = await fetch(`/image/${imageId}/thumbnail`);
            if (response.ok) {
                const blob = await response.blob();
                const url = URL.createObjectURL(blob);
                const thumbnailImg = document.querySelector(`#img-${imageId} .thumbnail-preview`);
                if (thumbnailImg) {
                    thumbnailImg.src = url;
                    thumbnailImg.alt = 'Thumbnail';
                }
            }
        } catch (error) {
            console.error('Error loading thumbnail:', error);
        }
    }
    
    // Load watermarked image
    if (info.watermark_status === 'done') {
        try {
            const response = await fetch(`/image/${imageId}/watermarked`);
            if (response.ok) {
                const blob = await response.blob();
                const url = URL.createObjectURL(blob);
                const watermarkedImg = document.querySelector(`#img-${imageId} .watermarked-preview`);
                if (watermarkedImg) {
                    watermarkedImg.src = url;
                    watermarkedImg.alt = 'Watermarked image';
                }
            }
        } catch (error) {
            console.error('Error loading watermarked image:', error);
        }
    }
}

async function deleteImage(imageId) {
    if (!confirm('Are you sure you want to delete this image?')) {
        return;
    }
    
    try {
        const response = await fetch(`/image/${imageId}`, {
            method: 'DELETE'
        });
        
        if (response.ok) {
            // Remove from DOM
            const imageCard = document.getElementById(`img-${imageId}`);
            if (imageCard) {
                imageCard.remove();
            }
            
            // Clean up
            if (pollingIntervals.has(imageId)) {
                clearInterval(pollingIntervals.get(imageId));
                pollingIntervals.delete(imageId);
            }
            uploadedImages.delete(imageId);
            
            updateNoImagesMessage();
            showNotification('Image deleted successfully', 'success');
        } else {
            const result = await response.json();
            showNotification(result.error || 'Delete failed', 'error');
        }
    } catch (error) {
        console.error('Delete error:', error);
        showNotification('Delete failed. Please try again.', 'error');
    }
}

async function viewImageInfo(imageId) {
    try {
        const response = await fetch(`/image/${imageId}/info`);
        const data = await response.json();
        
        if (response.ok) {
            const info = `
Image Information:
ID: ${data.id}
Overall Status: ${data.status}

Processing Status:
• Resize: ${data.resize_status || 'pending'}
• Thumbnail: ${data.thumbnail_status || 'pending'}
• Watermark: ${data.watermark_status || 'pending'}

File Paths:
• Original: ${data.original_path || 'N/A'}
• Processed: ${data.processed_path || 'N/A'}
• Thumbnail: ${data.thumbnail_path || 'N/A'}
• Watermarked: ${data.watermarked_path || 'N/A'}
            `;
            alert(info);
        } else {
            showNotification(data.error || 'Failed to get image info', 'error');
        }
    } catch (error) {
        console.error('Info error:', error);
        showNotification('Failed to get image info', 'error');
    }
}

async function downloadImage(imageId) {
    try {
        const response = await fetch(`/image/${imageId}`);
        
        if (response.ok) {
            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `processed-image-${imageId}.jpg`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
            
            showNotification('Download started', 'success');
        } else {
            const data = await response.json();
            showNotification(data.error || 'Download failed', 'error');
        }
    } catch (error) {
        console.error('Download error:', error);
        showNotification('Download failed', 'error');
    }
}

async function downloadAll(imageId) {
    try {
        const endpoints = [
            { url: `/image/${imageId}/original`, name: `${imageId}_original` },
            { url: `/image/${imageId}`, name: `${imageId}_resized` },
            { url: `/image/${imageId}/thumbnail`, name: `${imageId}_thumbnail` },
            { url: `/image/${imageId}/watermarked`, name: `${imageId}_watermarked` }
        ];
        
        let downloadCount = 0;
        
        for (const endpoint of endpoints) {
            try {
                const response = await fetch(endpoint.url);
                if (response.ok) {
                    const blob = await response.blob();
                    const url = URL.createObjectURL(blob);
                    const a = document.createElement('a');
                    a.href = url;
                    a.download = `${endpoint.name}.jpg`;
                    document.body.appendChild(a);
                    a.click();
                    document.body.removeChild(a);
                    URL.revokeObjectURL(url);
                    downloadCount++;
                    
                    // Small delay between downloads
                    await new Promise(resolve => setTimeout(resolve, 500));
                }
            } catch (error) {
                console.error(`Error downloading ${endpoint.name}:`, error);
            }
        }
        
        showNotification(`Downloaded ${downloadCount} files`, 'success');
    } catch (error) {
        console.error('Download all error:', error);
        showNotification('Download failed', 'error');
    }
}

async function openImageModal(imageId, imageType = 'resized') {
    try {
        let endpoint;
        switch (imageType) {
            case 'original':
                endpoint = `/image/${imageId}/original`;
                break;
            case 'thumbnail':
                endpoint = `/image/${imageId}/thumbnail`;
                break;
            case 'watermarked':
                endpoint = `/image/${imageId}/watermarked`;
                break;
            case 'resized':
            default:
                endpoint = `/image/${imageId}`;
                break;
        }
        
        const response = await fetch(endpoint);
        
        if (response.ok) {
            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            modalImage.src = url;
            modal.style.display = 'block';
        } else {
            // Fallback to original image if requested type is not available
            const originalResponse = await fetch(`/image/${imageId}/original`);
            if (originalResponse.ok) {
                const blob = await originalResponse.blob();
                const url = URL.createObjectURL(blob);
                modalImage.src = url;
                modal.style.display = 'block';
            } else {
                showNotification('Image not available for full view', 'error');
            }
        }
    } catch (error) {
        console.error('Modal error:', error);
        showNotification('Failed to load full image', 'error');
    }
}

function closeImageModal() {
    modal.style.display = 'none';
    modalImage.src = '';
}

function updateNoImagesMessage() {
    if (uploadedImages.size === 0) {
        noImagesMessage.style.display = 'block';
        imagesContainer.style.display = 'none';
    } else {
        noImagesMessage.style.display = 'none';
        imagesContainer.style.display = 'grid';
    }
}

function showNotification(message, type = 'info') {
    // Create notification element
    const notification = document.createElement('div');
    notification.className = `notification notification-${type}`;
    notification.textContent = message;
    
    // Style the notification
    Object.assign(notification.style, {
        position: 'fixed',
        top: '20px',
        right: '20px',
        padding: '12px 24px',
        borderRadius: '8px',
        color: 'white',
        fontWeight: '500',
        zIndex: '9999',
        maxWidth: '400px',
        boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
        transform: 'translateX(100%)',
        transition: 'transform 0.3s ease'
    });
    
    // Set background color based on type
    const colors = {
        success: '#28a745',
        error: '#dc3545',
        info: '#17a2b8',
        warning: '#ffc107'
    };
    notification.style.backgroundColor = colors[type] || colors.info;
    
    document.body.appendChild(notification);
    
    // Animate in
    setTimeout(() => {
        notification.style.transform = 'translateX(0)';
    }, 100);
    
    // Auto remove after 5 seconds
    setTimeout(() => {
        notification.style.transform = 'translateX(100%)';
        setTimeout(() => {
            if (notification.parentNode) {
                notification.parentNode.removeChild(notification);
            }
        }, 300);
    }, 5000);
}

// Individual processing triggers
async function triggerResize(imageId) {
    try {
        const response = await fetch(`/image/${imageId}/resize`, {
            method: 'POST'
        });
        
        const result = await response.json();
        
        if (response.ok || response.status === 202) {
            showNotification(result.message || 'Resize processing started', 'success');
            // Start polling to update status
            setTimeout(() => startPolling(imageId), 1000);
        } else {
            showNotification(result.error || 'Failed to start resize', 'error');
        }
    } catch (error) {
        console.error('Resize trigger error:', error);
        showNotification('Failed to start resize processing', 'error');
    }
}

async function triggerThumbnail(imageId) {
    try {
        const response = await fetch(`/image/${imageId}/thumbnail`, {
            method: 'POST'
        });
        
        const result = await response.json();
        
        if (response.ok || response.status === 202) {
            showNotification(result.message || 'Thumbnail processing started', 'success');
            // Start polling to update status
            setTimeout(() => startPolling(imageId), 1000);
        } else {
            showNotification(result.error || 'Failed to start thumbnail', 'error');
        }
    } catch (error) {
        console.error('Thumbnail trigger error:', error);
        showNotification('Failed to start thumbnail processing', 'error');
    }
}

async function triggerWatermark(imageId) {
    try {
        const response = await fetch(`/image/${imageId}/watermark`, {
            method: 'POST'
        });
        
        const result = await response.json();
        
        if (response.ok || response.status === 202) {
            showNotification(result.message || 'Watermark processing started', 'success');
            // Start polling to update status
            setTimeout(() => startPolling(imageId), 1000);
    } else {
            showNotification(result.error || 'Failed to start watermark', 'error');
        }
    } catch (error) {
        console.error('Watermark trigger error:', error);
        showNotification('Failed to start watermark processing', 'error');
    }
}

// Clean up on page unload
window.addEventListener('beforeunload', function() {
    pollingIntervals.forEach(interval => clearInterval(interval));
});