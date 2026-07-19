/**
 * AuthManager
 * 全局认证状态管理：当 token 失效（401）时通过回调列表广播事件，
 * UI 层（EntryAbility/Index）监听后跳转登录页并清空当前用户信息。
 *
 * ArkTS 没有像 Kotlin SharedFlow 那样的流，使用回调列表 + 简单 emit 实现。
 */
export type AuthEventHandler = (event: AuthEvent) => void;

export enum AuthEvent {
  /** token 失效，需要重新登录 */
  TOKEN_EXPIRED,
}

export class AuthManager {
  private static instance: AuthManager | null = null;
  private handlers: Set<AuthEventHandler> = new Set<AuthEventHandler>();

  private constructor() {}

  static getInstance(): AuthManager {
    if (AuthManager.instance === null) {
      AuthManager.instance = new AuthManager();
    }
    return AuthManager.instance;
  }

  /** 注册监听器，返回反注册函数 */
  on(handler: AuthEventHandler): () => void {
    this.handlers.add(handler);
    return () => { this.handlers.delete(handler); };
  }

  /** 广播事件 */
  emit(event: AuthEvent): void {
    this.handlers.forEach((h) => {
      try {
        h(event);
      } catch (e) {
        console.error(`[AuthManager] handler error: ${JSON.stringify(e)}`);
      }
    });
  }

  notifyTokenExpired(): void {
    this.emit(AuthEvent.TOKEN_EXPIRED);
  }
}

export const authManager = AuthManager.getInstance();
