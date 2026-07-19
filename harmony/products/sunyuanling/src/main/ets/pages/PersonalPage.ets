import router from '@ohos.router';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { AppRoutes } from '../router/AppRoutes';
import { TokenManager } from '../token/tokenManager';

/**
 * PersonalPage
 * 个人中心：头像 + 用户名 + 邮箱 + 资料编辑/修改密码/服务器设置/同步列表/传输列表入口 + 登出。
 * 对标 Android ui/screen/person/* 简化合并版。
 */
@Component
export struct PersonalPage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;

  async doLogout(): Promise<void> {
    await userStore.clear();
    await TokenManager.getInstance().clear();
    router.replaceUrl({ url: AppRoutes.LOGIN });
  }

  build() {
    Column() {
      Text('我的').fontSize(24).fontWeight(FontWeight.Bold).margin({ top: 20, bottom: 16 });

      // 用户卡片
      Row() {
        Text(this.currentUser?.username?.charAt(0)?.toUpperCase() ?? 'U')
          .width(64).height(64)
          .textAlign(TextAlign.Center)
          .fontSize(28)
          .fontColor('#FFFFFF')
          .backgroundColor('#007AFF')
          .borderRadius(32);
        Column() {
          Text(this.currentUser?.username ?? '未登录')
            .fontSize(18).fontWeight(FontWeight.Medium);
          Text(this.currentUser?.email ?? (this.currentUser?.phone ?? ''))
            .fontSize(13).fontColor('#9E9E9E').margin({ top: 4 });
          if (this.currentUser?.role === 'admin') {
            Text('管理员').fontSize(11).fontColor('#FF9500')
              .padding({ left: 6, right: 6, top: 2, bottom: 2 })
              .border({ width: 1, color: '#FF9500' })
              .borderRadius(4).margin({ top: 4 });
          }
        }.margin({ left: 12 }).alignItems(HorizontalAlign.Start);
        Blank();
      }
      .width('90%')
      .padding(16)
      .backgroundColor('#FFFFFF')
      .borderRadius(12)
      .alignItems(VerticalAlign.Center);

      // 菜单
      Column() {
        this.menuItem('编辑资料', () => { router.pushUrl({ url: AppRoutes.EDIT_PROFILE }); });
        this.menuItem('修改密码', () => { router.pushUrl({ url: AppRoutes.CHANGE_PASSWORD }); });
        this.menuItem('服务器地址', () => { router.pushUrl({ url: AppRoutes.SERVER_SETTINGS }); });
        this.menuItem('同步列表', () => { router.pushUrl({ url: AppRoutes.SYNC_LIST }); });
        this.menuItem('传输列表', () => { router.pushUrl({ url: AppRoutes.TRANSFERS }); });
        this.menuItem('设置', () => { router.pushUrl({ url: AppRoutes.SETTINGS }); });
      }
      .width('90%')
      .backgroundColor('#FFFFFF')
      .borderRadius(12)
      .margin({ top: 16 });

      Button('退出登录')
        .type(ButtonType.Capsule)
        .width('90%')
        .height(44)
        .backgroundColor('#FF3B30')
        .margin({ top: 24 })
        .onClick(() => { this.doLogout(); });

      Blank();
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5')
    .alignItems(HorizontalAlign.Center);
  }

  @Builder
  menuItem(label: string, action: () => void) {
    Row() {
      Text(label).fontSize(16);
      Blank();
      Text('›').fontSize(20).fontColor('#9E9E9E');
    }
    .width('100%')
    .padding({ left: 16, right: 16, top: 14, bottom: 14 })
    .border({ width: { bottom: 1 }, color: '#F0F0F0' })
    .onClick(() => { action(); });
  }
}

export default PersonalPage;
