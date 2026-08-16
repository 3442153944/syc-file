import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';

/**
 * AppConfig
 * 鸿蒙端运行期配置，对标 Android AppConfig + ConfigManager。
 * 默认值与 Android 保持一致（ddns.sunyuanling.cn:8891 https），
 * 通过 preferences 持久化，运行时可热修改并广播到 Request 单例。
 *
 * ArkTS 限制：不允许通过索引访问字段、不允许 spread、不允许把对象字面量当类型。
 * 故将每个配置项作为类的实例属性显式声明，持久化时显式逐字段读写。
 */
export enum LogLevel {
  DEBUGGER,
  INFO,
  WARN,
  ERROR,
}

const PREF_NAME = 'app_config';

export type ConfigChangedHandler = () => void;

export class AppConfig {
  private static inst: AppConfig | null = null;
  private pref: preferences.Preferences | null = null;
  private changeHandlers: Set<ConfigChangedHandler> = new Set<ConfigChangedHandler>();

  // ====== 配置字段（默认值与 Android AppConfig 对齐） ======
  server: string = 'ddns.sunyuanling.cn';
  routePath: string = '/file';
  isHttps: boolean = true;
  port: number = 8443;
  caPath: string = '';
  connectTimeoutMs: number = 30000;
  readTimeoutMs: number = 30000;
  // 以下两个已废弃：WS 改为固定 3s 无限重连（见 net/WebSocketManager 的 RECONNECT_INTERVAL_MS），
  // 唯一停机条件是 token 失效。字段保留仅为兼容旧持久化，不再被读取。
  wsReconnectIntervalMs: number = 3000;
  wsMaxReconnectAttempts: number = -1;
  uploadChunkSize: number = 4 * 1024 * 1024;
  maxConcurrentUploads: number = 3;
  maxConcurrentDownloads: number = 3;
  // 已弃用：下载位置改由用户授权的云梯目录决定（见 storage/CloudLadderStorage），此字段保留仅为兼容旧持久化。
  downloadDir: string = '';
  autoSyncEnabled: boolean = true;
  syncOnWifiOnly: boolean = true;
  forceKeepAliveEnabled: boolean = false;
  loggerLevel: number = LogLevel.DEBUGGER;
  logDir: string = 'FileSync/log';
  logMaxFileSizeBytes: number = 10 * 1024 * 1024;
  logMaxFileCount: number = 5;
  filePageSize: number = 50;
  versionName: string = '1.0.0';

  private constructor() {}

  static getInstance(): AppConfig {
    if (AppConfig.inst === null) {
      AppConfig.inst = new AppConfig();
    }
    return AppConfig.inst;
  }

  /** 在 EntryAbility onCreate 中调用，加载持久化配置 */
  async init(context: common.Context): Promise<void> {
    try {
      this.pref = await preferences.getPreferences(context, PREF_NAME);
      this.server = (await this.pref.get('server', this.server)) as string;
      this.routePath = (await this.pref.get('routePath', this.routePath)) as string;
      this.isHttps = (await this.pref.get('isHttps', this.isHttps)) as boolean;
      this.port = (await this.pref.get('port', this.port)) as number;
      this.caPath = (await this.pref.get('caPath', this.caPath)) as string;
      this.connectTimeoutMs = (await this.pref.get('connectTimeoutMs', this.connectTimeoutMs)) as number;
      this.readTimeoutMs = (await this.pref.get('readTimeoutMs', this.readTimeoutMs)) as number;
      this.wsReconnectIntervalMs = (await this.pref.get('wsReconnectIntervalMs', this.wsReconnectIntervalMs)) as number;
      this.wsMaxReconnectAttempts = (await this.pref.get('wsMaxReconnectAttempts', this.wsMaxReconnectAttempts)) as number;
      this.uploadChunkSize = (await this.pref.get('uploadChunkSize', this.uploadChunkSize)) as number;
      this.maxConcurrentUploads = (await this.pref.get('maxConcurrentUploads', this.maxConcurrentUploads)) as number;
      this.maxConcurrentDownloads = (await this.pref.get('maxConcurrentDownloads', this.maxConcurrentDownloads)) as number;
      this.downloadDir = (await this.pref.get('downloadDir', this.downloadDir)) as string;
      this.autoSyncEnabled = (await this.pref.get('autoSyncEnabled', this.autoSyncEnabled)) as boolean;
      this.syncOnWifiOnly = (await this.pref.get('syncOnWifiOnly', this.syncOnWifiOnly)) as boolean;
      this.forceKeepAliveEnabled = (await this.pref.get('forceKeepAliveEnabled', this.forceKeepAliveEnabled)) as boolean;
      this.loggerLevel = (await this.pref.get('loggerLevel', this.loggerLevel)) as number;
      this.logDir = (await this.pref.get('logDir', this.logDir)) as string;
      this.logMaxFileSizeBytes = (await this.pref.get('logMaxFileSizeBytes', this.logMaxFileSizeBytes)) as number;
      this.logMaxFileCount = (await this.pref.get('logMaxFileCount', this.logMaxFileCount)) as number;
      this.filePageSize = (await this.pref.get('filePageSize', this.filePageSize)) as number;
      this.versionName = (await this.pref.get('versionName', this.versionName)) as string;
    } catch (err) {
      const e = err as BusinessError;
      console.error(`[AppConfig] init failed: code=${e.code}, msg=${e.message}`);
    }
    this.notifyChanged();
  }

  /** 持久化当前所有字段 */
  async save(): Promise<void> {
    if (this.pref === null) return;
    try {
      await this.pref.put('server', this.server);
      await this.pref.put('routePath', this.routePath);
      await this.pref.put('isHttps', this.isHttps);
      await this.pref.put('port', this.port);
      await this.pref.put('caPath', this.caPath);
      await this.pref.put('connectTimeoutMs', this.connectTimeoutMs);
      await this.pref.put('readTimeoutMs', this.readTimeoutMs);
      await this.pref.put('wsReconnectIntervalMs', this.wsReconnectIntervalMs);
      await this.pref.put('wsMaxReconnectAttempts', this.wsMaxReconnectAttempts);
      await this.pref.put('uploadChunkSize', this.uploadChunkSize);
      await this.pref.put('maxConcurrentUploads', this.maxConcurrentUploads);
      await this.pref.put('maxConcurrentDownloads', this.maxConcurrentDownloads);
      await this.pref.put('downloadDir', this.downloadDir);
      await this.pref.put('autoSyncEnabled', this.autoSyncEnabled);
      await this.pref.put('syncOnWifiOnly', this.syncOnWifiOnly);
      await this.pref.put('forceKeepAliveEnabled', this.forceKeepAliveEnabled);
      await this.pref.put('loggerLevel', this.loggerLevel);
      await this.pref.put('logDir', this.logDir);
      await this.pref.put('logMaxFileSizeBytes', this.logMaxFileSizeBytes);
      await this.pref.put('logMaxFileCount', this.logMaxFileCount);
      await this.pref.put('filePageSize', this.filePageSize);
      await this.pref.put('versionName', this.versionName);
      await this.pref.flush();
    } catch (err) {
      console.error(`[AppConfig] save failed: ${JSON.stringify(err)}`);
    }
    this.notifyChanged();
  }

  /** 修改部分字段并持久化（手动赋值，避免 spread） */
  async update(
    server?: string,
    routePath?: string,
    isHttps?: boolean,
    port?: number,
    autoSyncEnabled?: boolean,
    syncOnWifiOnly?: boolean,
    forceKeepAliveEnabled?: boolean,
    downloadDir?: string,
    caPath?: string
  ): Promise<void> {
    if (server !== undefined) this.server = server;
    if (routePath !== undefined) this.routePath = routePath;
    if (isHttps !== undefined) this.isHttps = isHttps;
    if (port !== undefined) this.port = port;
    if (caPath !== undefined) this.caPath = caPath;
    if (autoSyncEnabled !== undefined) this.autoSyncEnabled = autoSyncEnabled;
    if (syncOnWifiOnly !== undefined) this.syncOnWifiOnly = syncOnWifiOnly;
    if (forceKeepAliveEnabled !== undefined) this.forceKeepAliveEnabled = forceKeepAliveEnabled;
    if (downloadDir !== undefined) this.downloadDir = downloadDir;
    await this.save();
  }

  /** 恢复网络相关配置为代码默认值并持久化（用于排查旧 prefs 覆盖问题） */
  async resetToDefaults(): Promise<void> {
    this.server = 'ddns.sunyuanling.cn';
    this.routePath = '/file';
    this.isHttps = true;
    this.port = 8443;
    this.caPath = '';
    await this.save();
  }

  onChange(handler: ConfigChangedHandler): () => void {
    this.changeHandlers.add(handler);
    return () => { this.changeHandlers.delete(handler); };
  }

  private notifyChanged(): void {
    this.changeHandlers.forEach((h) => {
      try { h(); } catch (e) { console.error(`[AppConfig] handler: ${JSON.stringify(e)}`); }
    });
  }

  // ====== 工具函数 ======
  getBaseUrl(): string {
    const protocol = this.isHttps ? 'https' : 'http';
    const route = this.routePath.replace(/\/+$/, '');
    const url = `${protocol}://${this.server}:${this.port}${route}`;
    console.info(`[AppConfig] getBaseUrl=${url} (server=${this.server} port=${this.port} route='${this.routePath}' https=${this.isHttps})`);
    return url;
  }

  getWsUrl(path: string = '/v1/ws/connect'): string {
    const protocol = this.isHttps ? 'wss' : 'ws';
    const route = this.routePath.replace(/\/+$/, '');
    return `${protocol}://${this.server}:${this.port}${route}${path}`;
  }
}

export const appConfig = AppConfig.getInstance();
