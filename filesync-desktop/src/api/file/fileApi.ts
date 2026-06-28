import {invoke, isTauri} from '@tauri-apps/api/core'
import {httpPost, httpPostForm, buildGetUrl} from '../http'
import type {AvailableDisksData, TraverseDirectoryData, UploadData, DownloadHistoryData} from './fileTypes'

export async function getAvailableDisks(): Promise<AvailableDisksData> {
    if (isTauri()) return invoke<AvailableDisksData>('get_available_disks')
    return httpPost<AvailableDisksData>('/file/available-disks', {disk_path: '', detailed: true})
}

export async function traverseDirectory(path: string, page = 1, pageSize = 100): Promise<TraverseDirectoryData> {
    if (isTauri()) return invoke<TraverseDirectoryData>('traverse_directory', {path, page, pageSize})
    return httpPost<TraverseDirectoryData>('/file/traverse-directory', {path, page, page_size: pageSize})
}

/**
 * Tauri 模式：传本地绝对路径，Rust 读文件做 multipart
 * Web   模式：传 File 对象，浏览器做 FormData
 */
export async function uploadFile(localPathOrFile: string | File, remoteDir: string): Promise<UploadData> {
    if (isTauri()) {
        return invoke<UploadData>('upload_file', {localPath: localPathOrFile as string, remoteDir})
    }
    const file = localPathOrFile as File
    const form = new FormData()
    form.append('action', 'upload')
    form.append('path', remoteDir)
    form.append('name', file.name)
    form.append('file', file)
    return httpPostForm<UploadData>('/file/upload', form)
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
