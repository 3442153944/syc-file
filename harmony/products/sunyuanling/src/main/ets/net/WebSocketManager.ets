import { webSocket } from '@kit.NetworkKit';
import { appConfig } from '../app/AppConfig';
import { TokenManager } from '../token/tokenManager';
import { deviceInfoSvc } from '../util/DeviceInfo';
import { authManager, AuthEvent } from './AuthManager';
import type { WsMessageEnvelope } from '../api/sync/SyncTypes';

/**
 * WebSocketManager
 * 鸿蒙端 WebSocket 管理器，对标 Android network/websocket.kt。
 *
 * - 连接 wss://host:port/file/v1/ws/connect，token 放 query（后端 Token 头 + ?token= 都支持）
 * - 设备 id/name/version 经 query 上报（device_id 必填，用于任务定向派发）
 * - 指数退避重连（1.5s × 2^n，上限 30s，最大次数取自 AppConfig.wsMaxReconnectAttempts，负数=无限）
 * - 消息通过回调列表分发（同步引擎订阅）；状态变化亦广播
 *
 * 注：ArkTS 没有 SharedFlow，用回调列表 + 一次性快照实现。
 */

export enum WsState {
  CONNECTING = 'connecting',
  CONNECTED = 'connected',
  DISCONNECTED = 'disconnected',
  ERROR = 'error',
}

export interface WsMessage {
  text: string;        // 原始文本（Binary 消息也以 utf-8 解码为 text，同步协议全是 JSON 文本）
  parsed: WsMessageEnvelope | null;
}

export type WsStateHandler = (state: WsState, errorMsg: string) => void;
export type WsMessageHandler = (msg: WsMessage) => void;

class WebSocketManagerClass {
  private ws: webSocket.WebSocket | null = null;
  private isConnecting: boolean = false;
  private shouldReconnect: boolean = false;
  private reconnectAttempts: number = 0;
  private reconnectTimerId: number = -1;
  private stateHandlers: Set<WsStateHandler> = new Set<WsStateHandler>();
  private messageHandlers: Set<WsMessageHandler> = new Set<WsMessageHandler>();
  private currentState: WsState = WsState.DISCONNECTED;
  private unregAuth: (() => void) | null = null;

  /** 注册状态变化监听 */
  onState(handler: WsStateHandler): () => void {
    this.stateHandlers.add(handler);
    return () => { this.stateHandlers.delete(handler); };
  }

  /** 注册消息监听 */
  onMessage(handler: WsMessageHandler): () => void {
    this.messageHandlers.add(handler);
    return () => { this.messageHandlers.delete(handler); };
  }

  getState(): WsState {
    return this.currentState;
  }

  isConnected(): boolean {
    return this.currentState === WsState.CONNECTED;
  }

  private setState(s: WsState, errMsg: string = ''): void {
    this.currentState = s;
    this.stateHandlers.forEach((h) => {
      try { h(s, errMsg); } catch (e) { /* ignore */ }
    });
  }

  private emitMessage(text: string): void {
    let parsed: WsMessageEnvelope | null = null;
    try {
      parsed = JSON.parse(text) as WsMessageEnvelope;
    } catch (e) {
      parsed = null;
    }
    const msg: WsMessage = { text, parsed };
    this.messageHandlers.forEach((h) => {
      try { h(msg); } catch (e) { /* ignore */ }
    });
  }

  /** 连接（带 token + 设备信息 query） */
  async connect(): Promise<void> {
    if (this.isConnecting || this.currentState === WsState.CONNECTED) return;
    this.shouldReconnect = true;
    this.reconnectAttempts = 0;

    const token: string = TokenManager.getInstance().getToken();
    if (token.length === 0) {
      this.setState(WsState.ERROR, 'Token 为空');
      return;
    }
    await this.doConnect(token);
  }

  private async doConnect(token: string): Promise<void> {
    if (this.isConnecting) return;
    this.isConnecting = true;
    this.cleanupOld();

    const deviceId: string = deviceInfoSvc.getDeviceId();
    const deviceName: string = 'HarmonyOS';
    const platform: string = 'harmony';
    const appVersion: string = appConfig.versionName;
    const baseUrl: string = appConfig.getWsUrl();
    const queryParts: string[] = [
      `token=${encodeURIComponent(token)}`,
      `device_id=${encodeURIComponent(deviceId)}`,
      `device_name=${encodeURIComponent(deviceName)}`,
      `device_type=harmony`,
      `platform=${encodeURIComponent(platform)}`,
      `app_version=${encodeURIComponent(appVersion)}`,
    ];
    const url: string = `${baseUrl}?${queryParts.join('&')}`;

    this.setState(WsState.CONNECTING);

    try {
      const ws: webSocket.WebSocket = webSocket.createWebSocket();
      this.ws = ws;

      // 注册事件回调（ArkTS webSocket.on 是 callback-style）
      ws.on('open', () => {
        this.isConnecting = false;
        this.reconnectAttempts = 0;
        this.setState(WsState.CONNECTED);
      });
      ws.on('message', (err: Error, value: string | ArrayBuffer) => {
        if (err) { return; }
        if (typeof value === 'string') {
          this.emitMessage(value);
        } else if (value instanceof ArrayBuffer) {
          try {
            const text: string = decodeBinaryToString(value);
            this.emitMessage(text);
          } catch (e) { /* ignore */ }
        }
      });
      ws.on('close', () => {
        this.isConnecting = false;
        this.setState(WsState.DISCONNECTED);
        if (this.shouldReconnect) this.scheduleReconnect(token);
      });
      ws.on('error', (err: Error) => {
        this.isConnecting = false;
        const msg: string = err ? err.message : 'unknown ws error';
        this.setState(WsState.ERROR, msg);
        if (this.shouldReconnect) this.scheduleReconnect(token);
      });

      const opts: webSocket.WebSocketRequestOptions = {
        header: {
          'Token': token,
          'User-Agent': 'FileSyncHarmony/1.0.0',
          'X-Device-Id': deviceId,
        } as Record<string, string>,
      };
      await ws.connect(url, opts);
    } catch (e) {
      this.isConnecting = false;
      const msg: string = (e as Error)?.message ?? JSON.stringify(e);
      this.setState(WsState.ERROR, msg);
      if (this.shouldReconnect) this.scheduleReconnect(token);
    }
  }

  /** 安排重连：指数退避，上限 30s，maxAttempts<0 时无限 */
  private scheduleReconnect(token: string): void {
    const max: number = appConfig.wsMaxReconnectAttempts;
    if (max >= 0 && this.reconnectAttempts >= max) {
      this.shouldReconnect = false;
      this.setState(WsState.ERROR, '已达到最大重连次数');
      return;
    }
    const backoff: number = Math.min(this.reconnectAttempts, 14);
    const delayMs: number = Math.min(1500 * (1 << backoff), 30000);
    this.reconnectAttempts += 1;
    if (this.reconnectTimerId !== -1) {
      clearTimeout(this.reconnectTimerId);
    }
    this.reconnectTimerId = setTimeout(() => {
      this.reconnectTimerId = -1;
      if (this.shouldReconnect) {
        // 重新取 token（可能已被刷新）
        const t: string = TokenManager.getInstance().getToken();
        if (t.length === 0) {
          this.shouldReconnect = false;
          this.setState(WsState.ERROR, 'Token 已失效');
          return;
        }
        this.doConnect(t).catch((e: Error) => {
          console.error(`[WebSocketManager] reconnect failed: ${e.message}`);
        });
      }
    }, delayMs);
  }

  private cleanupOld(): void {
    if (this.ws !== null) {
      try {
        this.ws.off('open');
        this.ws.off('message');
        this.ws.off('close');
        this.ws.off('error');
        this.ws.close();
      } catch (e) { /* ignore */ }
      this.ws = null;
    }
  }

  /** 主动断开（不再重连） */
  disconnect(): void {
    this.shouldReconnect = false;
    if (this.reconnectTimerId !== -1) {
      clearTimeout(this.reconnectTimerId);
      this.reconnectTimerId = -1;
    }
    this.cleanupOld();
    this.setState(WsState.DISCONNECTED);
  }

  /** 发送文本消息 */
  async send(text: string): Promise<boolean> {
    if (this.ws === null) return false;
    try {
      await this.ws.send(text);
      return true;
    } catch (e) {
      return false;
    }
  }

  /** 启动 401 监听：token 失效时自动断开 WS */
  registerAuthListener(): void {
    if (this.unregAuth !== null) return;
    this.unregAuth = authManager.on((ev: AuthEvent) => {
      if (ev === AuthEvent.TOKEN_EXPIRED) {
        this.disconnect();
      }
    });
  }

  unregisterAuthListener(): void {
    if (this.unregAuth !== null) {
      this.unregAuth();
      this.unregAuth = null;
    }
  }
}

/** ArrayBuffer → utf-8 string（不使用 Function.apply，逐字节解码） */
function decodeBinaryToString(buf: ArrayBuffer): string {
  const u8: Uint8Array = new Uint8Array(buf);
  let s: string = '';
  // 8K chunk 防止堆栈溢出
  const chunkSize: number = 0x8000;
  for (let i = 0; i < u8.length; i += chunkSize) {
    const end: number = Math.min(i + chunkSize, u8.length);
    let chunk: string = '';
    for (let j = i; j < end; j++) {
      chunk += String.fromCharCode(u8[j]);
    }
    s += chunk;
  }
  return decodeURIComponent(escape(s));
}

export const wsManager = new WebSocketManagerClass();
