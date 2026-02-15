/** Max thumbnail dimension in pixels for client-side preview. */
export const THUMB_MAX_PX = 160;

/** Generate a thumbnail data URL for an image file using canvas. */
export async function generateImageThumbnail(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      const scale = Math.min(
        THUMB_MAX_PX / img.width,
        THUMB_MAX_PX / img.height,
        1,
      );
      const w = Math.round(img.width * scale);
      const h = Math.round(img.height * scale);
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        URL.revokeObjectURL(url);
        reject(new Error("Canvas not supported"));
        return;
      }
      ctx.drawImage(img, 0, 0, w, h);
      URL.revokeObjectURL(url);
      resolve(canvas.toDataURL("image/jpeg", 0.7));
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("Failed to load image"));
    };
    img.src = url;
  });
}

/** Generate a thumbnail data URL for a video by extracting a frame. */
export async function generateVideoThumbnail(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const video = document.createElement("video");
    video.preload = "metadata";
    video.muted = true;
    video.playsInline = true;

    video.onloadeddata = () => {
      video.currentTime = Math.min(1, video.duration / 2);
    };
    video.onseeked = () => {
      const scale = Math.min(
        THUMB_MAX_PX / video.videoWidth,
        THUMB_MAX_PX / video.videoHeight,
        1,
      );
      const w = Math.round(video.videoWidth * scale);
      const h = Math.round(video.videoHeight * scale);
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        URL.revokeObjectURL(url);
        reject(new Error("Canvas not supported"));
        return;
      }
      ctx.drawImage(video, 0, 0, w, h);
      URL.revokeObjectURL(url);
      resolve(canvas.toDataURL("image/jpeg", 0.7));
    };
    video.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("Failed to load video"));
    };
    video.src = url;
  });
}

/** Best-effort thumbnail generation — returns undefined on failure. */
export async function generateThumbnail(
  file: File,
): Promise<string | undefined> {
  try {
    if (file.type.startsWith("video/")) {
      return await generateVideoThumbnail(file);
    }
    if (file.type.startsWith("image/")) {
      return await generateImageThumbnail(file);
    }
  } catch {
    // Thumbnail generation is best-effort (HEIC may fail on non-macOS, etc.)
  }
  return undefined;
}
