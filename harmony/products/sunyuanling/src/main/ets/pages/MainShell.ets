import router from '@ohos.router';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { AppRoutes } from '../router/AppRoutes';
import { authManager, AuthEvent } from '../net/AuthManager';
import { TokenManager } from '../token/tokenManager';
import { syncEngine } from '../sync/SyncEngine';
import HomePage from './HomePage';
import FilesPage from './FilesPage';
import MonitorPage from './MonitorPage';
import PersonalPage from './PersonalPage';

/**
 * MainShell
 * 主导航容器：底部 4 标签（Home/Files/Monitor/Personal），对标 Android 底部 NavigationSuiteScaffold。
 * 监听 401 事件，收到后清用户 + 跳登录页。
 */
@Entry
@Component
struct MainShell {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;
  @State selectedIndex: number = 0;
  private unregAuth: (() => void) | null = null;

  aboutToAppear(): void {
    this.unregAuth = authManager.on((ev: AuthEvent) => {
      if (ev === AuthEvent.TOKEN_EXPIRED) {
        this.handleTokenExpired();
      }
    });
    // 启动同步引擎（download_only：连 WS + 拉取 folder + 接 task_created）
    try {
      syncEngine.start(this.getUIContext().getHostContext() as Context);
    } catch (e) {
      console.warn(`[MainShell] syncEngine.start failed: ${(e as Error)?.message ?? JSON.stringify(e)}`);
    }
  }

  aboutToDisappear(): void {
    if (this.unregAuth) {
      this.unregAuth();
      this.unregAuth = null;
    }
    try { syncEngine.stop(); } catch (e) { /* ignore */ }
  }

  private async handleTokenExpired(): Promise<void> {
    await userStore.clear();
    await TokenManager.getInstance().clear();
    router.replaceUrl({ url: AppRoutes.LOGIN });
  }

  build() {
    Column() {
      Tabs({ barPosition: BarPosition.End, index: this.selectedIndex }) {
        TabContent() {
          HomePage();
        }
        .tabBar(this.tabBarItem('首页', 0));

        TabContent() {
          FilesPage();
        }
        .tabBar(this.tabBarItem('文件', 1));

        TabContent() {
          MonitorPage();
        }
        .tabBar(this.tabBarItem('监控', 2));

        TabContent() {
          PersonalPage();
        }
        .tabBar(this.tabBarItem('我的', 3));
      }
      .barMode(BarMode.Fixed)
      .onChange((idx: number) => { this.selectedIndex = idx; })
      .width('100%')
      .height('100%');
    }
    .width('100%')
    .height('100%');
  }

  @Builder
  tabBarItem(label: string, idx: number) {
    Column() {
      Text(label)
        .fontSize(20)
        .fontColor(this.selectedIndex === idx ? '#007AFF' : '#9E9E9E')
        .fontWeight(this.selectedIndex === idx ? FontWeight.Bold : FontWeight.Regular);
    }.width('100%').justifyContent(FlexAlign.Center).padding({ top: 8, bottom: 8 });
  }
}

export default MainShell;
