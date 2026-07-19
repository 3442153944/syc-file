/**
 * ApiRoutes
 * 职责：定义全部 API 路由常量，严格对应后端路由表（new_server/internal/handler/routers.go 与 internal/sync/router.go）。
 * 路径以 /v1 为基准前缀（由 Request 单例拼上），即这里写的是 /v1 之后的部分。
 * 路径参数（:id、:conn_id、:device_id、:name）以 %s 占位符表示，由调用方 format 填入。
 */
export class ApiRoutes {
  // ==================== 公开路由（无需 Token） ====================
  static readonly PING_ROOT_GET: string = '/ping'; // GET /ping，根路径无 /v1 前缀
  static readonly PING: string = '/ping'; // POST /v1/ping
  static readonly USER_REGISTER: string = '/user/register';
  static readonly USER_LOGIN: string = '/user/login';
  static readonly USER_RESET_PASSWORD: string = '/user/reset-password';
  static readonly USER_VERIFY: string = '/user/verify';

  // ==================== 需鉴权路由 ====================
  static readonly USER_UPDATE_INFO: string = '/user/update-info';
  static readonly USER_CHANGE_PASSWORD: string = '/user/change-password';

  // 文件
  static readonly FILE_AVAILABLE_DISKS: string = '/file/available-disks';
  static readonly FILE_TRAVERSE_DIRECTORY: string = '/file/traverse-directory';
  static readonly FILE_DOWNLOAD: string = '/file/download';
  static readonly FILE_DOWNLOAD_HISTORY: string = '/file/download-history';
  static readonly FILE_DELETE_DOWNLOAD_HISTORY: string = '/file/delete-download-history';
  static readonly FILE_DELETE: string = '/file/delete';

  // 分片上传
  static readonly FILE_UPLOAD_INIT: string = '/file/upload/init';
  static readonly FILE_UPLOAD_CHUNK: string = '/file/upload/chunk';
  static readonly FILE_UPLOAD_STATUS: string = '/file/upload/status';
  static readonly FILE_UPLOAD_COMPLETE: string = '/file/upload/complete';

  // WebSocket
  static readonly WS_CONNECT: string = '/ws/connect'; // GET /v1/ws/connect
  static readonly WS_ONLINE: string = '/ws/online'; // admin
  static readonly WS_MY_DEVICES: string = '/ws/my-devices'; // 当前用户自己的在线设备
  static readonly WS_USER_CONNECTIONS: string = '/ws/user/%s/connections';
  static readonly WS_STATS: string = '/ws/stats';
  static readonly WS_SEND: string = '/ws/send';
  static readonly WS_BROADCAST: string = '/ws/broadcast';
  static readonly WS_DISCONNECT_CONN: string = '/ws/conn/%s';
  static readonly WS_DISCONNECT_USER: string = '/ws/user/%s';
  static readonly WS_DISCONNECT_DEVICE: string = '/ws/device/%s';
  static readonly WS_GROUP: string = '/ws/group';
  static readonly WS_GROUP_SEND: string = '/ws/group/send';
  static readonly WS_GROUP_USERS: string = '/ws/group/%s/users';

  // ==================== 同步（internal/sync/router.go） ====================
  static readonly SYNC_NOTIFY: string = '/sync/notify';
  static readonly SYNC_SCAN: string = '/sync/scan';
  static readonly SYNC_TASK_COMPLETE: string = '/sync/tasks/%s/complete';
  static readonly SYNC_TASK_FAILED: string = '/sync/tasks/%s/failed';
  static readonly SYNC_TASK_BLOCKED: string = '/sync/tasks/%s/blocked';
  static readonly SYNC_TASKS: string = '/sync/tasks';
  static readonly SYNC_TASKS_PENDING: string = '/sync/tasks/pending';
  static readonly SYNC_CONFLICTS: string = '/sync/conflicts';
  static readonly SYNC_CONFLICT_RESOLVE: string = '/sync/conflicts/%s/resolve';
  static readonly SYNC_CONFLICT_DELETE: string = '/sync/conflicts/%s';
  static readonly SYNC_FOLDERS: string = '/sync/folders';

  // ==================== 应用更新（未接线后端时仅占位） ====================
  static readonly UPDATE_CHECK: string = '/update/check';
  static readonly UPDATE_LATEST: string = '/update/latest';
}

/**
 * 格式化路径，将 %s 替换为传入的实参。
 * ArkTS 不支持变参，这里最多支持 3 个参数，覆盖现有所有路由。
 */
export function formatRoute(template: string, ...args: string[]): string {
  let result: string = template;
  for (let i = 0; i < args.length; i++) {
    result = result.replace('%s', encodeURIComponent(args[i]));
  }
  return result;
}
