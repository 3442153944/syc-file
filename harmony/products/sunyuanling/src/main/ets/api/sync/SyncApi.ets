import { Request } from '../../net/Request';
import { ApiRoutes, formatRoute } from '../../constants/ApiRoutes';
import { ApiResponse } from '../../net/Response';
import type {
  SyncTaskInfo,
  SyncConflictInfo,
  SyncFolderInfo,
  ResolveConflictParams,
  SyncNotifyParams,
  SyncScanParams,
  TaskCompleteParams,
  TaskFailedParams,
  TaskBlockedParams,
} from './SyncTypes';

/**
 * SyncApi
 * 同步域 REST 调用，对标 Android api/sync/SyncApi.kt，对应后端 /v1/sync/*。
 */
export class SyncApi {
  /** 同步任务记录（可选按 status/device_id 过滤，limit 上限） */
  static async listTasks(
    status?: string,
    deviceId?: string,
    limit?: number
  ): Promise<ApiResponse<SyncTaskInfo[]>> {
    const q: Record<string, string> = {};
    if (status !== undefined && status.length > 0) q['status'] = status;
    if (deviceId !== undefined && deviceId.length > 0) q['device_id'] = deviceId;
    if (limit !== undefined && limit > 0) q['limit'] = String(limit);
    return await Request.get<ApiResponse<SyncTaskInfo[]>>(ApiRoutes.SYNC_TASKS, q);
  }

  /** 本设备待执行任务（后端按 target_device_id 过滤 pending 态） */
  static async pendingTasks(deviceId: string): Promise<ApiResponse<SyncTaskInfo[]>> {
    const q: Record<string, string> = { 'device_id': deviceId };
    return await Request.get<ApiResponse<SyncTaskInfo[]>>(ApiRoutes.SYNC_TASKS_PENDING, q);
  }

  /** 当前用户的待处理冲突列表 */
  static async listConflicts(): Promise<ApiResponse<SyncConflictInfo[]>> {
    return await Request.get<ApiResponse<SyncConflictInfo[]>>(ApiRoutes.SYNC_CONFLICTS);
  }

  /** 解决冲突 */
  static async resolveConflict(id: number, resolution: string): Promise<ApiResponse<Object>> {
    const route: string = formatRoute(ApiRoutes.SYNC_CONFLICT_RESOLVE, String(id));
    const params: ResolveConflictParams = { resolution };
    return await Request.post<ApiResponse<Object>>(route, params as Object);
  }

  /** 删除冲突记录 */
  static async deleteConflict(id: number): Promise<ApiResponse<Object>> {
    const route: string = formatRoute(ApiRoutes.SYNC_CONFLICT_DELETE, String(id));
    return await Request.delete<ApiResponse<Object>>(route);
  }

  /** 同步文件夹列表 */
  static async listFolders(): Promise<ApiResponse<SyncFolderInfo[]>> {
    return await Request.get<ApiResponse<SyncFolderInfo[]>>(ApiRoutes.SYNC_FOLDERS);
  }

  // ===== 任务执行回报 =====

  /** 任务完成（download 回传落盘 hash 供服务端校验） */
  static async completeTask(id: number, fileHash: string = ''): Promise<ApiResponse<Object>> {
    const route: string = formatRoute(ApiRoutes.SYNC_TASK_COMPLETE, String(id));
    const params: TaskCompleteParams = { file_hash: fileHash };
    return await Request.post<ApiResponse<Object>>(route, params as Object);
  }

  /** 任务失败 */
  static async failTask(id: number, error: string): Promise<ApiResponse<Object>> {
    const route: string = formatRoute(ApiRoutes.SYNC_TASK_FAILED, String(id));
    const params: TaskFailedParams = { error };
    return await Request.post<ApiResponse<Object>>(route, params as Object);
  }

  /** 目标被占用：转 waiting_unlock，不计入重试次数 */
  static async blockTask(id: number, reason: string): Promise<ApiResponse<Object>> {
    const route: string = formatRoute(ApiRoutes.SYNC_TASK_BLOCKED, String(id));
    const params: TaskBlockedParams = { reason };
    return await Request.post<ApiResponse<Object>>(route, params as Object);
  }

  // ===== 探测上报（鸿蒙 download_only 客户端一般不用，但保留接口） =====

  static async notify(params: SyncNotifyParams): Promise<ApiResponse<Object>> {
    return await Request.post<ApiResponse<Object>>(ApiRoutes.SYNC_NOTIFY, params as Object);
  }

  static async scan(params: SyncScanParams): Promise<ApiResponse<Object>> {
    return await Request.post<ApiResponse<Object>>(ApiRoutes.SYNC_SCAN, params as Object);
  }
}
