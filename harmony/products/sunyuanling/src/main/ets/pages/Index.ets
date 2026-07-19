import router from '@ohos.router';
import { TokenManager } from '../token/tokenManager';
import { UserApi } from '../api/user/UserApi';
import { userStore } from '../app/UserStore';
import { AppRoutes } from '../router/AppRoutes';

/**
 * Index
 * 启动入口页：根据 token 存在与否决定跳转 Login 或 MainShell。
 * 启动时若有 token，异步调 verify 校正本地缓存用户（对标 Android MainActivity.AppInitializer）。
 */
@Entry
@Component
struct Index {
  @State booting: boolean = true;
  @State hint: string = '正在启动...';

  async aboutToAppear(): Promise<void> {
    const token = TokenManager.getInstance().getToken();
    if (token.length === 0) {
      router.replaceUrl({ url: AppRoutes.LOGIN });
      return;
    }
    // 有 token，异步 verify 校正
    try {
      this.hint = '正在校验登录状态...';
      const resp = await UserApi.verify();
      if (resp.code === 200 && resp.data) {
        await userStore.setCurrent(resp.data);
        router.replaceUrl({ url: AppRoutes.MAIN_SHELL });
      } else {
        await userStore.clear();
        await TokenManager.getInstance().clear();
        router.replaceUrl({ url: AppRoutes.LOGIN });
      }
    } catch (e) {
      // 网络异常或 token 失效：若本地有缓存的用户信息，仍允许进入主界面
      // （离线容错，登录后 verify 失败不强制重登；下次请求 401 由 AuthManager 处理）
      if (userStore.getCurrent() !== null) {
        router.replaceUrl({ url: AppRoutes.MAIN_SHELL });
      } else {
        await TokenManager.getInstance().clear();
        router.replaceUrl({ url: AppRoutes.LOGIN });
      }
    } finally {
      this.booting = false;
    }
  }

  build() {
    Column() {
      LoadingProgress().width(40).height(40);
      Text(this.hint).fontSize(14).fontColor('#9E9E9E').margin({ top: 12 });
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#FFFFFF')
    .justifyContent(FlexAlign.Center)
    .alignItems(HorizontalAlign.Center);
  }
}

export default Index;
