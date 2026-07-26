import router from '@ohos.router';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { AppRoutes } from '../router/AppRoutes';
import { TokenManager } from '../token/tokenManager';
import { appConfig } from '../app/AppConfig';

/**
 * PersonalPage
 * 个人中心：头像（服务器加载）+ 用户名 + 角色 + 详细信息 + 功能入口 + 登出。
 * 对标 Android ui/screen/person/UserInfoScreen.kt。
 */
@Component
export struct PersonalPage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;

  async doLogout(): Promise<void> {
    await userStore.clear();
    await TokenManager.getInstance().clear();
    router.replaceUrl({ url: AppRoutes.LOGIN });
  }

  private avatarUrl(): string {
    const avatar = this.currentUser?.avatar ?? '';
    if (avatar.length === 0) return '';
    if (avatar.startsWith('http')) return avatar;
    return `${appConfig.getBaseUrl()}/static/${avatar}`;
  }

  private formatTime(t: string | null): string {
    if (t === null || t.length === 0) return '—';
    try {
      const d = new Date(t);
      return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
    } catch (e) {
      return t;
    }
  }

  build() {
    Scroll() {
      Column() {
        Text('我的').fontSize(24).fontWeight(FontWeight.Bold).margin({ top: 20, bottom: 16 });

        // 用户卡片
        Row() {
          if (this.avatarUrl().length > 0) {
            Image(this.avatarUrl())
              .width(64).height(64)
              .borderRadius(32)
              .objectFit(ImageFit.Cover);
          } else {
            Text(this.currentUser?.username?.charAt(0)?.toUpperCase() ?? 'U')
              .width(64).height(64)
              .textAlign(TextAlign.Center)
              .fontSize(28)
              .fontColor('#FFFFFF')
              .backgroundColor('#007AFF')
              .borderRadius(32);
          }
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

        // 账户信息
        Column() {
          this.infoRow('用户 ID', `${this.currentUser?.id ?? '—'}`);
          this.infoRow('账号状态', this.currentUser?.status === 1 ? '正常' : '已禁用');
          this.infoRow('上次登录', this.formatTime(this.currentUser?.last_login ?? null));
          this.infoRow('注册时间', this.formatTime(this.currentUser?.created_at ?? null));
        }
        .width('90%')
        .backgroundColor('#FFFFFF')
        .borderRadius(12)
        .margin({ top: 12 })
        .padding({ left: 16, right: 16, top: 8, bottom: 8 });

        // 菜单
        Column() {
          this.menuItem('编辑资料', () => { router.pushUrl({ url: AppRoutes.EDIT_PROFILE }); });
          this.menuItem('修改密码', () => { router.pushUrl({ url: AppRoutes.CHANGE_PASSWORD }); });
          this.menuItem('服务器地址', () => { router.pushUrl({ url: AppRoutes.SERVER_SETTINGS }); });
          this.menuItem('同步文件夹', () => { router.pushUrl({ url: AppRoutes.SYNC_FOLDER_MAP }); });
          this.menuItem('同步列表', () => { router.pushUrl({ url: AppRoutes.SYNC_LIST }); });
          this.menuItem('传输列表', () => { router.pushUrl({ url: AppRoutes.TRANSFERS }); });
          this.menuItem('设置', () => { router.pushUrl({ url: AppRoutes.SETTINGS }); });
          // 同步台账、后台暂存文件都在沙盒里，给用户一个看得见管得着的入口
          this.menuItem('沙盒空间', () => { router.pushUrl({ url: AppRoutes.SANDBOX }); });
        }
        .width('90%')
        .backgroundColor('#FFFFFF')
        .borderRadius(12)
        .margin({ top: 12 });

        Button('退出登录')
          .type(ButtonType.Capsule)
          .width('90%')
          .height(44)
          .backgroundColor('#FF3B30')
          .margin({ top: 24, bottom: 32 })
          .onClick(() => { this.doLogout(); });
      }
      .width('100%')
      .alignItems(HorizontalAlign.Center);
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5')
    .scrollBar(BarState.Off);
  }

  @Builder
  infoRow(label: string, value: string) {
    Row() {
      Text(label).fontSize(14).fontColor('#9E9E9E');
      Blank();
      Text(value).fontSize(14);
    }
    .width('100%')
    .padding({ top: 10, bottom: 10 })
    .border({ width: { bottom: 1 }, color: '#F5F5F5' });
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
