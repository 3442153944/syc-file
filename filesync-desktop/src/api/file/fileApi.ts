import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, httpGet, buildGetUrl} from '../http'
import {getDeviceId} from '../platform'
import {uploadChunked as uploadChunkedWeb, DEFAULT_CHUNK_SIZE, DEFAULT_CONCURRENCY} from './chunkedUploader'
import type {AvailableDisksData, TraverseDirectoryData, UploadCompleteData, DownloadHistoryData, CreateShareLinkData, ShareLinkListData} from './fileTypes'

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
        // 分片上传整个在 Rust 里执行，进度由 Rust 记进传输状态（transfers.rs），
        // 通过 transfer-changed 事件推给页面（见 useTransferStore）；这里的 onProgress 不会被调用。
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

/**
 * 缩略图 URL（图片 / 视频封面 / 音频专辑封面），可直接放进 <img>。
 * width 服务端只认 128 / 256 / 512；version 传文件大小：内容变了 URL 就变，
 * 缩略图响应带 1 天缓存期，不带版本号的话文件刚被改动的一天内会一直看到旧图。
 */
export async function buildThumbnailUrl(path: string, name: string, width = 256, version = 0): Promise<string> {
    if (isTauri()) return invoke<string>('build_thumbnail_url', {path, name, width, version})
    return buildGetUrl('/file/thumbnail', {path, name, w: String(width), ...(version ? {v: String(version)} : {})})
}

/** 文本文件此刻的内容和版本。content 统一成 \n 换行、不含 BOM；换行风格和 BOM 单独给出，服务端保存时还原。 */
export interface TextFileData {
    content: string
    /** 版本号（整文件 blake3），保存时作为 base_hash 交回，服务端据此发现别人改过 */
    hash: string
    size: number
    mtime_ms: number
    eol: 'lf' | 'crlf'
    bom: boolean
}

export interface SaveTextResult {
    saved: boolean
    /** 保存时发现别人在此期间改过：没有覆盖，current 是对方的最新内容和版本号 */
    conflict?: boolean
    /** 内容没变，没有写盘 */
    unchanged?: boolean
    hash?: string
    size?: number
    mtime_ms?: number
    /** 文件在同步文件夹内，已通知各设备拉取 */
    synced?: boolean
    current?: TextFileData
}

/** 读取文本文件（配置文件、TXT、日志、代码等；上限 2MB，非 UTF-8 / 二进制会被拒绝）。 */
export async function readTextFile(path: string, name: string): Promise<TextFileData> {
    if (isTauri()) return invoke<TextFileData>('api_request', {method: 'GET', path: '/file/text/read', body: null, query: {path, name}})
    return httpGet<TextFileData>('/file/text/read', {path, name})
}

/**
 * 保存文本文件。正常保存带 base_hash（读到时的版本号）；用户明确选择覆盖他人修改时传 force。
 * 版本冲突不是异常，而是 saved=false + conflict=true 的正常返回，调用方据此合并。
 */
export async function saveTextFile(params: {
    path: string
    name: string
    content: string
    base_hash?: string
    force?: boolean
}): Promise<SaveTextResult> {
    if (isTauri()) return invoke<SaveTextResult>('api_request', {method: 'POST', path: '/file/text/save', body: params, query: null})
    return httpPost<SaveTextResult>('/file/text/save', params)
}

export async function getDownloadHistory(pageNum: number, pageSize: number): Promise<DownloadHistoryData> {
    if (isTauri()) return invoke<DownloadHistoryData>('get_download_history', {pageNum, pageSize})
    return httpPost<DownloadHistoryData>('/file/download-history', {pageNum, pageSize})
}

export async function deleteDownloadHistory(ids: number[]): Promise<void> {
    if (isTauri()) return invoke('delete_download_history', {ids})
    await httpPost('/file/delete-download-history', {ids})
}

export async function createShareLink(path: string, name: string, expireMinutes: number): Promise<CreateShareLinkData> {
    if (isTauri()) return invoke<CreateShareLinkData>('api_request', {
        method: 'POST',
        path: '/file/share-link/create',
        body: {path, name, expire_minutes: expireMinutes},
        query: null,
    })
    return httpPost<CreateShareLinkData>('/file/share-link/create', {path, name, expire_minutes: expireMinutes})
}

export async function listShareLinks(pageNum: number, pageSize: number): Promise<ShareLinkListData> {
    if (isTauri()) return invoke<ShareLinkListData>('api_request', {
        method: 'POST',
        path: '/file/share-link/list',
        body: {pageNum, pageSize},
        query: null,
    })
    return httpPost<ShareLinkListData>('/file/share-link/list', {pageNum, pageSize})
}

export async function revokeShareLink(shareCode: string): Promise<void> {
    if (isTauri()) {
        await invoke('api_request', {
            method: 'POST',
            path: '/file/share-link/revoke',
            body: {share_code: shareCode},
            query: null,
        })
        return
    }
    await httpPost('/file/share-link/revoke', {share_code: shareCode})
}
