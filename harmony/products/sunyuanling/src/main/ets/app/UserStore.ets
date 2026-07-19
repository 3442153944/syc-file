import type { UserInfo } from '../api/user/UserTypes';
import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';

/**
 * UserStore
 * 进程级可观察当前用户单例（对标 Android ui/viewModel/user/UserStore.kt）。
 * 通过 AppStorage 暴露给 UI 层做响应式订阅（@StorageLink('currentUser')）。
 * 同时持久化用户信息到 preferences，便于启动时跳过 verify 直接展示（启动后异步 verify 校正）。
 */
const STORAGE_KEY = 'currentUser';
const PREF_NAME = 'user_store';
const PREF_KEY = 'cached_user';

export class UserStore {
  private static inst: UserStore | null = null;
  private pref: preferences.Preferences | null = null;

  private constructor() {}

  static getInstance(): UserStore {
    if (UserStore.inst === null) {
      UserStore.inst = new UserStore();
    }
    return UserStore.inst;
  }

  async init(context: common.Context): Promise<void> {
    try {
      this.pref = await preferences.getPreferences(context, PREF_NAME);
      const cached = await this.pref.get(PREF_KEY, '');
      if (cached && (cached as string).length > 0) {
        const user = JSON.parse(cached as string) as UserInfo;
        AppStorage.setOrCreate<UserInfo | null>(STORAGE_KEY, user);
      } else {
        AppStorage.setOrCreate<UserInfo | null>(STORAGE_KEY, null);
      }
    } catch (err) {
      const e = err as BusinessError;
      console.error(`[UserStore] init failed: ${e.code} ${e.message}`);
    }
  }

  /** 设置当前用户（登录/verify 成功后调用），同时持久化 */
  async setCurrent(user: UserInfo): Promise<void> {
    AppStorage.setOrCreate<UserInfo | null>(STORAGE_KEY, user);
    if (this.pref) {
      try {
        await this.pref.put(PREF_KEY, JSON.stringify(user));
        await this.pref.flush();
      } catch (e) {
        console.error(`[UserStore] persist failed: ${JSON.stringify(e)}`);
      }
    }
  }

  getCurrent(): UserInfo | null {
    const v = AppStorage.get<UserInfo>(STORAGE_KEY);
    return v === undefined ? null : v;
  }

  isAdmin(): boolean {
    const u = this.getCurrent();
    return u !== null && u.role === 'admin';
  }

  /** 清除当前用户（登出/401 时调用） */
  async clear(): Promise<void> {
    AppStorage.setOrCreate<UserInfo | null>(STORAGE_KEY, null);
    if (this.pref) {
      try {
        await this.pref.delete(PREF_KEY);
        await this.pref.flush();
      } catch (e) { /* ignore */ }
    }
  }
}

export const userStore = UserStore.getInstance();
export { STORAGE_KEY as USER_STORAGE_KEY };
