/**
 * AppRoutes
 * 鸿蒙端页面路由常量，对应 main_pages.json 中的 src 列表。
 * 使用 @ohos.router 的 pushUrl/replUrl 跳转。
 */
export class AppRoutes {
  static readonly INDEX: string = 'pages/Index'; // 启动入口（按用户/token 分流）
  static readonly LOGIN: string = 'pages/LoginPage';
  static readonly MAIN_SHELL: string = 'pages/MainShell';
  static readonly HOME: string = 'pages/HomePage';
  static readonly FILES: string = 'pages/FilesPage';
  static readonly MONITOR: string = 'pages/MonitorPage';
  static readonly PERSONAL: string = 'pages/PersonalPage';
  static readonly TRANSFERS: string = 'pages/TransfersPage';
  static readonly SYNC_LIST: string = 'pages/SyncListPage';
  static readonly SETTINGS: string = 'pages/SettingsPage';
  static readonly SERVER_SETTINGS: string = 'pages/ServerSettingsPage';
  static readonly EDIT_PROFILE: string = 'pages/EditProfilePage';
  static readonly CHANGE_PASSWORD: string = 'pages/ChangePasswordPage';
  static readonly FILE_BROWSE: string = 'pages/FileBrowsePage';
  static readonly UPLOAD: string = 'pages/UploadPage';
  static readonly DEVICE_LIST: string = 'pages/DeviceListPage';
}
