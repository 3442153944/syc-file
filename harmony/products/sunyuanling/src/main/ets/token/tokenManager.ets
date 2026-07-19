import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';

export class TokenManager {
  private static instance: TokenManager | null = null;
  private static readonly PREF_NAME: string = 'auth_storage';
  private static readonly KEY_TOKEN: string = 'user_token';
  private pref: preferences.Preferences | null = null;
  private memoryToken: string = ""; // 增加内存缓存，提升请求时的读取性能

  private constructor() {}

  /**
   * 获取单例实例
   */
  public static getInstance(): TokenManager {
    if (TokenManager.instance === null) {
      TokenManager.instance = new TokenManager();
    }
    return TokenManager.instance;
  }

  /**
   * 初始化存储库 (必须在 EntryAbility 的 onCreate 中调用)
   */
  public async init(context: common.Context): Promise<void> {
    try {
      // 获取首选项实例
      this.pref = await preferences.getPreferences(context, TokenManager.PREF_NAME);
      // 初始化时同步一次内存缓存
      const val = await this.pref.get(TokenManager.KEY_TOKEN, "");
      this.memoryToken = val as string;
    } catch (err) {
      const error = err as BusinessError;
      console.error(`[TokenManager] Init failed, code: ${error.code}, message: ${error.message}`);
    }
  }

  /**
   * 获取 Token (优先从内存读，极快)
   */
  public getToken(): string {
    return this.memoryToken;
  }

  /**
   * 存储 Token (登录成功调用)
   */
  public async setToken(token: string): Promise<void> {
    this.memoryToken = token;
    if (this.pref) {
      try {
        await this.pref.put(TokenManager.KEY_TOKEN, token);
        await this.pref.flush(); // 强制持久化到磁盘
      } catch (err) {
        throw new Error("Save Token Failed");
      }
    }
  }

  /**
   * 校验并更新 Token (应用进入时调用)
   */
  public async validateAndRefresh(newToken?: string): Promise<boolean> {
    if (this.pref === null) return false;

    // 如果有新 token 则更新，否则只验证是否存在
    if (newToken) {
      await this.setToken(newToken);
      return true;
    }

    return this.memoryToken !== "";
  }

  /**
   * 清除 Token (注销或 401 时调用)
   */
  public async clear(): Promise<void> {
    this.memoryToken = "";
    if (this.pref) {
      try {
        await this.pref.delete(TokenManager.KEY_TOKEN);
        await this.pref.flush();
      } catch (err) {
        console.error("[TokenManager] Clear failed");
      }
    }
  }
}