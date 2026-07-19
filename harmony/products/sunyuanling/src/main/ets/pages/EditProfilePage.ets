import router from '@ohos.router';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';
import { UserApi } from '../api/user/UserApi';
import { ResponseException } from '../net/Response';

/**
 * EditProfilePage
 * 编辑资料页：用户名 / 邮箱 / 手机（暂不支持头像，后端 multipart 接口待补）。
 */
@Entry
@Component
struct EditProfilePage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;
  @State username: string = '';
  @State email: string = '';
  @State phone: string = '';
  @State saving: boolean = false;
  @State resultMsg: string = '';

  aboutToAppear(): void {
    this.username = this.currentUser?.username ?? '';
    this.email = this.currentUser?.email ?? '';
    this.phone = this.currentUser?.phone ?? '';
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('编辑资料').fontSize(18).fontWeight(FontWeight.Bold);
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      Column() {
        Row() {
          Text('用户名').fontSize(14).width(80);
          TextInput({ text: this.username })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.username = v; });
        }.width('100%').margin({ bottom: 12 });
        Row() {
          Text('邮箱').fontSize(14).width(80);
          TextInput({ text: this.email })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.email = v; });
        }.width('100%').margin({ bottom: 12 });
        Row() {
          Text('手机号').fontSize(14).width(80);
          TextInput({ text: this.phone })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.phone = v; });
        }.width('100%').margin({ bottom: 24 });
        // 注：后端 update-info 是 multipart，目前 Request 单例不支持 multipart。
        // TODO: 接入 multipart 后启用真正的更新调用。
        Button(this.saving ? '保存中...' : '保存（暂未实现）')
          .type(ButtonType.Capsule)
          .width('100%').height(44)
          .backgroundColor('#9E9E9E')
          .enabled(false);
        if (this.resultMsg.length > 0) {
          Text(this.resultMsg).fontSize(13).margin({ top: 8 });
        }
      }
      .width('92%')
      .padding(16)
      .backgroundColor('#FFFFFF')
      .borderRadius(12)
      .margin({ top: 12 });
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }
}

export default EditProfilePage;
