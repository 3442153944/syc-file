import { Request } from '../../net/Request';
import { ApiRoutes } from '../../constants/ApiRoutes';
import { ApiResponse } from '../../net/Response';
import type {
  DeleteFileParams,
  AvailableDisksParams,
  AvailableDisksData,
  TraverseDirectoryParams,
  TraverseDirectoryData,
  DownloadParams,
  UploadInitParams,
  UploadInitData,
  UploadStatusData,
  UploadCompleteParams,
  UploadCompleteData,
  DownloadHistoryParams,
  DownloadHistoryData,
  DeleteDownloadHistoryParams,
} from './FileTypes';
import { appConfig } from '../../app/AppConfig';

/**
 * FileApi
 * 职责：文件相关 API 调用封装，每个函数只组装参数 + 调 Request，不含业务逻辑。
 * 对标 Android api/file/FileApi.kt。
 */
export class FileApi {
  /** 获取可用磁盘列表 */
  static async getAvailableDisks(params?: AvailableDisksParams): Promise<ApiResponse<AvailableDisksData>> {
    const p: AvailableDisksParams = params !== undefined && params !== null
      ? params
      : { disk_path: '', detailed: false } as AvailableDisksParams;
    return await Request.post<ApiResponse<AvailableDisksData>>(ApiRoutes.FILE_AVAILABLE_DISKS, p as Object);
  }

  /** 遍历目录 */
  static async traverseDirectory(params: TraverseDirectoryParams): Promise<ApiResponse<TraverseDirectoryData>> {
    return await Request.post<ApiResponse<TraverseDirectoryData>>(ApiRoutes.FILE_TRAVERSE_DIRECTORY, params as Object);
  }

  /** 删除远端单个文件 */
  static async deleteFile(params: DeleteFileParams): Promise<ApiResponse<Object>> {
    return await Request.post<ApiResponse<Object>>(ApiRoutes.FILE_DELETE, params as Object);
  }

  // ==================== 分片上传 ====================

  static async uploadInit(params: UploadInitParams): Promise<ApiResponse<UploadInitData>> {
    return await Request.post<ApiResponse<UploadInitData>>(ApiRoutes.FILE_UPLOAD_INIT, params as Object);
  }

  static async uploadStatus(uploadId: string): Promise<ApiResponse<UploadStatusData>> {
    const q: Record<string, string> = { 'upload_id': uploadId };
    return await Request.get<ApiResponse<UploadStatusData>>(ApiRoutes.FILE_UPLOAD_STATUS, q);
  }

  static async uploadComplete(params: UploadCompleteParams): Promise<ApiResponse<UploadCompleteData>> {
    return await Request.post<ApiResponse<UploadCompleteData>>(ApiRoutes.FILE_UPLOAD_COMPLETE, params as Object);
  }

  /**
   * 上传单分片：raw 字节 body，query 带 upload_id + index。
   *  - 200 → 成功
   *  - 422 → ChunkVerifyException
   *  - 404 → SessionGoneException
   */
  static async uploadChunk(uploadId: string, index: number, data: Uint8Array): Promise<Object> {
    return await Request.uploadChunk(ApiRoutes.FILE_UPLOAD_CHUNK, uploadId, index, data);
  }

  // ==================== 下载历史 ====================

  static async getDownloadHistory(params: DownloadHistoryParams): Promise<ApiResponse<DownloadHistoryData>> {
    return await Request.post<ApiResponse<DownloadHistoryData>>(ApiRoutes.FILE_DOWNLOAD_HISTORY, params as Object);
  }

  static async deleteDownloadHistory(params: DeleteDownloadHistoryParams): Promise<ApiResponse<Object>> {
    return await Request.post<ApiResponse<Object>>(ApiRoutes.FILE_DELETE_DOWNLOAD_HISTORY, params as Object);
  }

  // ==================== 下载 URL 构造 ====================

  /**
   * 构造下载 URL（token 放 query string，对标 Android FileApi.buildDownloadUrl）。
   * 用于让下载组件直接 GET，无需通过 Request 单例。
   */
  static buildDownloadUrl(params: DownloadParams): string {
    const base = `${appConfig.getBaseUrl()}/v1${ApiRoutes.FILE_DOWNLOAD}`;
    const token = ''; // 由 DownloadController 注入；这里仅占位
    const q: string[] = [];
    q.push(`path=${encodeURIComponent(params.path)}`);
    q.push(`name=${encodeURIComponent(params.name)}`);
    if (params.device_id && params.device_id.length > 0) {
      q.push(`device_id=${encodeURIComponent(params.device_id)}`);
    }
    if (token.length > 0) {
      q.push(`token=${encodeURIComponent(token)}`);
    }
    return `${base}?${q.join('&')}`;
  }
}
