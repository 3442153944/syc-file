// api/routes.rs
// 职责：定义全部 API 路由常量，严格对应后端路由表。
// 路径参数以格式化占位符 {} 表示，由调用方用 format!() 传入。
// 所有路径均相对于 /v1 前缀（client.rs 会自动拼接）。

// ── 公开路由（无需 Token） ─────────────────────────────────────────────────
pub const PING: &str = "/ping";
pub const USER_REGISTER: &str = "/user/register";
pub const USER_LOGIN: &str = "/user/login";
pub const USER_RESET_PASSWORD: &str = "/user/reset-password";
pub const USER_VERIFY: &str = "/user/verify";

// ── 用户（需 Token） ──────────────────────────────────────────────────────
pub const USER_UPDATE_INFO: &str = "/user/update-info";

// ── 文件（需 Token） ──────────────────────────────────────────────────────
pub const FILE_AVAILABLE_DISKS: &str = "/file/available-disks";
pub const FILE_TRAVERSE_DIRECTORY: &str = "/file/traverse-directory";
/// GET，支持 Range，参数拼 query string
pub const FILE_DOWNLOAD: &str = "/file/download";
/// POST multipart
pub const FILE_UPLOAD: &str = "/file/upload";
pub const FILE_DELETE: &str = "/file/delete";
pub const FILE_DOWNLOAD_HISTORY: &str = "/file/download-history";
pub const FILE_DELETE_DOWNLOAD_HISTORY: &str = "/file/delete-download-history";

// ── WebSocket ─────────────────────────────────────────────────────────────
/// GET，WebSocket 升级端点，参数拼 query string
pub const WS_CONNECT: &str = "/ws/connect";
pub const WS_ONLINE: &str = "/ws/online";
pub const WS_MY_DEVICES: &str = "/ws/my-devices";
/// 路径参数：user_id
pub const WS_USER_CONNECTIONS: &str = "/ws/user/{}/connections";
pub const WS_STATS: &str = "/ws/stats";
pub const WS_SEND: &str = "/ws/send";
pub const WS_BROADCAST: &str = "/ws/broadcast";
/// DELETE，路径参数：conn_id
pub const WS_DISCONNECT_CONN: &str = "/ws/conn/{}";
/// DELETE，路径参数：user_id
pub const WS_DISCONNECT_USER: &str = "/ws/user/{}";
/// DELETE，路径参数：device_id
pub const WS_DISCONNECT_DEVICE: &str = "/ws/device/{}";
pub const WS_GROUP: &str = "/ws/group";
pub const WS_GROUP_SEND: &str = "/ws/group/send";
/// 路径参数：group_name
pub const WS_GROUP_USERS: &str = "/ws/group/{}/users";

// ── 同步（需 Token） ──────────────────────────────────────────────────────
pub const SYNC_FOLDERS: &str = "/sync/folders";
/// 路径参数：folder_id
pub const SYNC_FOLDER_BY_ID: &str = "/sync/folders/{}";
/// HTTP 回退上报（WS 不可用时）
pub const SYNC_NOTIFY: &str = "/sync/notify";
pub const SYNC_SCAN: &str = "/sync/scan";
pub const SYNC_TASKS: &str = "/sync/tasks";
pub const SYNC_TASKS_PENDING: &str = "/sync/tasks/pending";
/// 路径参数：task_id
pub const SYNC_TASK_COMPLETE: &str = "/sync/tasks/{}/complete";
pub const SYNC_TASK_FAILED: &str = "/sync/tasks/{}/failed";
/// 路径参数：task_id；目标文件被占用 → 转 waiting_unlock
pub const SYNC_TASK_BLOCKED: &str = "/sync/tasks/{}/blocked";
pub const SYNC_CONFLICTS: &str = "/sync/conflicts";
/// 路径参数：conflict_id
pub const SYNC_CONFLICT_BY_ID: &str = "/sync/conflicts/{}";
/// 路径参数：conflict_id；解决冲突 accept_server / keep_local
pub const SYNC_CONFLICT_RESOLVE: &str = "/sync/conflicts/{}/resolve";
