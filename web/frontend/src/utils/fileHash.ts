/**
 * Content-based file deduplication using SHA-256 fingerprinting (DDR-067).
 *
 * Two-tier strategy:
 * 1. quickFingerprint — SHA-256 of (fileSize || first64KB || last64KB).
 *    Sub-millisecond even for multi-GB files.
 * 2. fullHash — SHA-256 of entire file contents (or sampled for very large files).
 *    Only needed when quick fingerprints collide.
 */

const FINGERPRINT_CHUNK = 64 * 1024; // 64 KB

function hexEncode(buffer: ArrayBuffer): string {
  return Array.from(new Uint8Array(buffer))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

/**
 * Compute a fast content fingerprint: SHA-256(fileSize || first64KB || last64KB).
 * Runs in sub-millisecond time regardless of file size.
 */
export async function quickFingerprint(file: File): Promise<string> {
  const sizeBytes = new ArrayBuffer(8);
  new DataView(sizeBytes).setFloat64(0, file.size);

  const first = await file
    .slice(0, Math.min(FINGERPRINT_CHUNK, file.size))
    .arrayBuffer();

  const lastStart = Math.max(0, file.size - FINGERPRINT_CHUNK);
  const last =
    file.size > FINGERPRINT_CHUNK
      ? await file.slice(lastStart).arrayBuffer()
      : new ArrayBuffer(0);

  const combined = new Uint8Array(
    sizeBytes.byteLength + first.byteLength + last.byteLength,
  );
  combined.set(new Uint8Array(sizeBytes), 0);
  combined.set(new Uint8Array(first), sizeBytes.byteLength);
  combined.set(
    new Uint8Array(last),
    sizeBytes.byteLength + first.byteLength,
  );

  const hash = await crypto.subtle.digest("SHA-256", combined);
  return hexEncode(hash);
}

/**
 * Compute a full content hash of the file.
 * For files ≤500 MB: reads and hashes the entire file.
 * For larger files: hashes 1 MB samples at 8 evenly-spaced offsets plus head/tail.
 */
export async function fullHash(file: File): Promise<string> {
  const MAX_FULL_READ = 500 * 1024 * 1024; // 500 MB

  if (file.size <= MAX_FULL_READ) {
    const buffer = await file.arrayBuffer();
    const hash = await crypto.subtle.digest("SHA-256", buffer);
    return hexEncode(hash);
  }

  const SAMPLE_SIZE = 1024 * 1024; // 1 MB
  const SAMPLE_COUNT = 8;
  const step = Math.floor(file.size / SAMPLE_COUNT);
  const parts: ArrayBuffer[] = [];

  const sizeBytes = new ArrayBuffer(8);
  new DataView(sizeBytes).setFloat64(0, file.size);
  parts.push(sizeBytes);

  for (let i = 0; i < SAMPLE_COUNT; i++) {
    const offset = i * step;
    const end = Math.min(offset + SAMPLE_SIZE, file.size);
    parts.push(await file.slice(offset, end).arrayBuffer());
  }
  parts.push(
    await file.slice(Math.max(0, file.size - SAMPLE_SIZE)).arrayBuffer(),
  );

  const totalLen = parts.reduce((sum, p) => sum + p.byteLength, 0);
  const combined = new Uint8Array(totalLen);
  let pos = 0;
  for (const part of parts) {
    combined.set(new Uint8Array(part), pos);
    pos += part.byteLength;
  }

  const hash = await crypto.subtle.digest("SHA-256", combined);
  return hexEncode(hash);
}
