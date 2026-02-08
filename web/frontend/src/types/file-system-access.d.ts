/**
 * File System Access API type declarations.
 *
 * Chrome supports these APIs but they are not yet in TypeScript's standard
 * DOM lib typings. Only the picker functions need declaring — the handle
 * types (FileSystemFileHandle, FileSystemDirectoryHandle) are already in
 * lib.dom.d.ts as of TypeScript 5.x.
 *
 * See: https://developer.mozilla.org/en-US/docs/Web/API/File_System_Access_API
 * DDR-029: File System Access API for Cloud Media Upload
 */

interface FilePickerAcceptType {
  description?: string;
  accept: Record<string, string | string[]>;
}

interface OpenFilePickerOptions {
  multiple?: boolean;
  excludeAcceptAllOption?: boolean;
  types?: FilePickerAcceptType[];
}

interface DirectoryPickerOptions {
  id?: string;
  mode?: "read" | "readwrite";
}

interface FileSystemDirectoryHandle {
  /** Iterate over directory entries (not in standard DOM lib typings). */
  values(): AsyncIterableIterator<
    FileSystemFileHandle | FileSystemDirectoryHandle
  >;
}

interface Window {
  showOpenFilePicker(
    options?: OpenFilePickerOptions,
  ): Promise<FileSystemFileHandle[]>;
  showDirectoryPicker(
    options?: DirectoryPickerOptions,
  ): Promise<FileSystemDirectoryHandle>;
}
