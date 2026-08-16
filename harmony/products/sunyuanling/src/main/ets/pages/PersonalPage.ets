import router from '@ohos.router';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { AppRoutes } from '../router/AppRoutes';
import { TokenManager } from '../token/tokenManager';
import { Request } from '../net/Request';
import { UserApi } from '../api/user/UserApi';

/**
 * PersonalPage
 * 个人中心：头像（服务器加载）+ 用户名 + 角色 + 详细信息 + 功能入口 + 登出。
 * 对标 Android ui/screen/person/UserInfoScreen.kt（基本信息 / 账户信息两张卡片，字段一一对齐）。
 */
@Component
export struct PersonalPage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;
  /** 头像加载失败（地址错/文件被删/服务不可达）→ 退回首字母占位，别留个白框 */
  @State avatarFailed: boolean = false;

  aboutToAppear(): void {
    // 缓存里的用户信息可能是旧版本存的（缺 last_login/created_at），进页面顺手校正一次
    this.refreshUser();
  }

  private async refreshUser(): Promise<void> {
    try {
      const resp = await UserApi.verify();
      if (resp.code === 200 && resp.data !== null) {
        await userStore.setCurrent(resp.data);
        this.avatarFailed = false;
      }
    } catch (e) {
      // 离线/服务不可达：继续用本地缓存展示，不打扰用户
      console.info(`[Personal] verify skipped: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  async doLogout(): Promise<void> {
    await userStore.clear();
    await TokenManager.getInstance().clear();
    router.replaceUrl({ url: AppRoutes.LOGIN });
  }

  /**
   * 头像地址。`avatar` 字段本身就是 `static/avatar/xxx.png`，
   * 只拼服务器根即可（对标 Android `"${Request.baseStaticUrl}/${user.avatar}"`）。
   */
  private avatarUrl(): string {
    return Request.staticUrl(this.currentUser?.avatar ?? '');
  }

  /** 空值统一显示「未设置」，对标 Android `?.ifBlank { "未设置" }`。 */
  private orUnset(v: string | null | undefined): string {
    return (v === null || v === undefined || v.trim().length === 0) ? '未设置' : v;
  }

  private formatTime(t: string | null): string {
    if (t === null || t === undefined || t.length === 0) return '未知';
    const d = new Date(t);
    const ms: number = d.getTime();
    // Go 的零值时间会序列化成 0001-01-01T00:00:00Z，展示成 1 年 1 月毫无意义
    if (isNaN(ms) || d.getFullYear() < 1980) return '未知';
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      + ` ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`;
  }

  build() {
    Scroll() {
      Column() {
        Text('我的').fontSize(24).fontWeight(FontWeight.Bold).margin({ top: 20, bottom: 16 });

        // 用户卡片
        Row() {
          if (this.avatarUrl().length > 0 && !this.avatarFailed) {
            Image(this.avatarUrl())
              .width(64).height(64)
              .borderRadius(32)
              .objectFit(ImageFit.Cover)
              .onError(() => {
                console.warn(`[Personal] 头像加载失败 url=${this.avatarUrl()}`);
                this.avatarFailed = true;
              });
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
            Text(this.orUnset(this.currentUser?.email ?? this.currentUser?.phone))
              .fontSize(13).fontColor('#9E9E9E').margin({ top: 4 });
            Text(this.currentUser?.role === 'admin' ? '管理员' : '普通用户')
              .fontSize(11)
              .fontColor(this.currentUser?.role === 'admin' ? '#FF9500' : '#007AFF')
              .padding({ left: 6, right: 6, top: 2, bottom: 2 })
              .border({ width: 1, color: this.currentUser?.role === 'admin' ? '#FF9500' : '#007AFF' })
              .borderRadius(4).margin({ top: 6 });
          }.margin({ left: 12 }).alignItems(HorizontalAlign.Start).layoutWeight(1);
        }
        .width('90%')
        .padding(16)
        .backgroundColor('#FFFFFF')
        .borderRadius(12)
        .alignItems(VerticalAlign.Center);

        // 基本信息（对齐 Android UserInfoScreen 的「基本信息」卡片）
        Column() {
          this.sectionTitle('基本信息');
          this.infoRow('用户 ID', `${this.currentUser?.id ?? 0}`);
          this.infoRow('用户名', this.orUnset(this.currentUser?.username));
          this.infoRow('邮箱', this.orUnset(this.currentUser?.email));
          this.infoRow('手机号', this.orUnset(this.currentUser?.phone));
        }
        .width('90%')
        .backgroundColor('#FFFFFF')
        .borderRadius(12)
        .margin({ top: 12 })
        .padding({ left: 16, right: 16, top: 8, bottom: 8 });

        // 账户信息（同上，「最后登录/注册时间」依赖服务端 login/verify 返回 last_login/created_at）
        Column() {
          this.sectionTitle('账户信息');
          this.infoRow('账户状态', this.currentUser?.status === 1 ? '正常' : '已禁用');
          this.infoRow('最后登录', this.formatTime(this.currentUser?.last_login ?? null));
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
  sectionTitle(title: string) {
    Text(title)
      .fontSize(14).fontWeight(FontWeight.Medium).fontColor('#007AFF')
      .width('100%')
      .padding({ top: 8, bottom: 4 });
  }

  @Builder
  infoRow(label: string, value: string) {
    Row() {
      Text(label).fontSize(14).fontColor('#9E9E9E');
      Blank();
      // 长邮箱/长用户名不能把行撑破：右对齐 + 单行省略
      Text(value).fontSize(14)
        .maxLines(1)
        .textOverflow({ overflow: TextOverflow.Ellipsis })
        .textAlign(TextAlign.End)
        .layoutWeight(1)
        .margin({ left: 12 });
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
