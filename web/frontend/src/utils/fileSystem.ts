/**
 * Shared file-system helpers for drag-and-drop and directory traversal.
 *
 * Replaces identical implementations in MediaUploader.tsx and FileUploader.tsx.
 * Uses the legacy `webkitGetAsEntry` / `FileSystemEntry` APIs for broad support.
 */

/** Read all entries from a FileSystemDirectoryReader (handles batching). */
export function readAllEntries(
  reader: FileSystemDirectoryReader,
): Promise<FileSystemEntry[]> {
  return new Promise((resolve, reject) => {
    const results: FileSystemEntry[] = [];
    function readBatch() {
      reader.readEntries((entries) => {
        if (entries.length === 0) {
          resolve(results);
        } else {
          results.push(...entries);
          readBatch(); // readEntries may return results in batches
        }
      }, reject);
    }
    readBatch();
  });
}

/** Convert a FileSystemFileEntry to a File. */
export function entryToFile(entry: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject));
}

/** Recursively collect all File objects from a FileSystemEntry tree. */
export async function collectFilesFromEntry(
  entry: FileSystemEntry,
): Promise<File[]> {
  if (entry.isFile) {
    try {
      const file = await entryToFile(entry as FileSystemFileEntry);
      return [file];
    } catch {
      return [];
    }
  }
  if (entry.isDirectory) {
    const reader = (entry as FileSystemDirectoryEntry).createReader();
    const children = await readAllEntries(reader);
    const nested = await Promise.all(children.map(collectFilesFromEntry));
    return nested.flat();
  }
  return [];
}

/** Extract files from a DataTransfer, recursively walking dropped directories. */
export async function getFilesFromDataTransfer(
  dataTransfer: DataTransfer,
): Promise<File[]> {
  const items = Array.from(dataTransfer.items);
  const entries = items
    .map((item) => item.webkitGetAsEntry?.())
    .filter((e): e is FileSystemEntry => e != null);

  // If webkitGetAsEntry is not supported, fall back to dataTransfer.files
  if (entries.length === 0) {
    return Array.from(dataTransfer.files);
  }

  const nested = await Promise.all(entries.map(collectFilesFromEntry));
  return nested.flat();
}
