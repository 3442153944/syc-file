import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, buildGetUrl} from '../http'
import {getDeviceId} from '../platform'
import {uploadChunked as uploadChunkedWeb, DEFAULT_CHUNK_SIZE, DEFAULT_CONCURRENCY} from './chunkedUploader'
import type {AvailableDisksData, TraverseDirectoryData, UploadCompleteData, DownloadHistoryData} from './fileTypes'

export async function getAvailableDisks(): Promise<AvailableDisksData> {
    if (isTauri()) return invoke<AvailableDisksData>('get_available_disks')
    return httpPost<AvailableDisksData>('/file/available-disks', {disk_path: '', detailed: true})
}

export async function traverseDirectory(path: string, page = 1, pageSize = 100): Promise<TraverseDirectoryData> {
    if (isTauri()) return invoke<TraverseDirectoryData>('traverse_directory', {path, page, pageSize})
    return httpPost<TraverseDirectoryData>('/file/traverse-directory', {path, page, page_size: pageSize})
}

/**
 * 上传文件（双端统一走分片协议：blake3 + Merkle 树根 + 乱序并发 + 断点续传 + 秒传）。
 *
 * Tauri 模式：传本地绝对路径，Rust 侧 chunked_uploader 实现分片。
 * Web   模式：传 File 对象，TS 侧 chunkedUploader 实现分片（@noble/hashes blake3，与 Rust 逐字节一致）。
 *
 * @param onProgress (已发送字节, 总字节)
 * @param onConflict 目标同名时：'reject'=服务端报错（默认，同步链路用）；
 *                   'timestamp'=服务端自动给文件名加时间戳（发布 APK 这类同名是常态的场景）
 */
export async function uploadFile(
    localPathOrFile: string | File,
    remoteDir: string,
    onProgress: (sent: number, total: number) => void = () => {},
    onConflict: 'reject' | 'timestamp' = 'reject',
): Promise<UploadCompleteData> {
    if (isTauri()) {
        // Rust command 内部已实现分片 + 进度事件 emit；这里不直接传进度回调
        // （Tauri command 无法把 JS 函数传入 Rust），调用方若需进度可监听 `upload-progress-byte` 事件。
        return invoke<UploadCompleteData>('upload_file', {
            localPath: localPathOrFile as string,
            remoteDir,
            onConflict,
        })
    }
    const file = localPathOrFile as File
    return uploadChunkedWeb(
        file,
        remoteDir,
        {chunkSize: DEFAULT_CHUNK_SIZE, concurrency: DEFAULT_CONCURRENCY, deviceId: getDeviceId(), onConflict},
        onProgress,
    )
}

export async function deleteFile(path: string, name: string): Promise<void> {
    if (isTauri()) return invoke('delete_file', {path, name})
    await httpPost('/file/delete', {path, name})
}

export async function buildDownloadUrl(path: string, name: string, deviceId: string): Promise<string> {
    if (isTauri()) return invoke<string>('build_download_url', {path, name, deviceId})
    return buildGetUrl('/file/download', {path, name, ...(deviceId ? {device_id: deviceId} : {})})
}

export async function getDownloadHistory(pageNum: number, pageSize: number): Promise<DownloadHistoryData> {
    if (isTauri()) return invoke<DownloadHistoryData>('get_download_history', {pageNum, pageSize})
    return httpPost<DownloadHistoryData>('/file/download-history', {pageNum, pageSize})
}

export async function deleteDownloadHistory(ids: number[]): Promise<void> {
    if (isTauri()) return invoke('delete_download_history', {ids})
    await httpPost('/file/delete-download-history', {ids})
}
