import router from '@ohos.router';
import { UserApi } from '../api/user/UserApi';
import { ResponseException } from '../net/Response';

/**
 * ChangePasswordPage
 * 修改密码页：旧密码 + 新密码（确认）→ POST /v1/user/change-password。
 */
@Entry
@Component
struct ChangePasswordPage {
  @State oldPwd: string = '';
  @State newPwd: string = '';
  @State confirmPwd: string = '';
  @State saving: boolean = false;
  @State resultMsg: string = '';
  @State isError: boolean = false;

  async submit(): Promise<void> {
    this.resultMsg = '';
    if (this.oldPwd.length === 0 || this.newPwd.length === 0) {
      this.resultMsg = '请填写完整';
      this.isError = true;
      return;
    }
    if (this.newPwd !== this.confirmPwd) {
      this.resultMsg = '两次新密码不一致';
      this.isError = true;
      return;
    }
    if (this.newPwd.length < 6) {
      this.resultMsg = '新密码至少 6 位';
      this.isError = true;
      return;
    }
    this.saving = true;
    try {
      const resp = await UserApi.changePassword({
        old_password: this.oldPwd,
        new_password: this.newPwd,
      });
      if (resp.code === 200) {
        this.resultMsg = '修改成功，请重新登录';
        this.isError = false;
        setTimeout(() => { router.replaceUrl({ url: 'pages/LoginPage' }); }, 1500);
      } else {
        this.resultMsg = resp.message || '修改失败';
        this.isError = true;
      }
    } catch (e) {
      this.resultMsg = e instanceof Error ? e.message : JSON.stringify(e);
      this.isError = true;
    } finally {
      this.saving = false;
    }
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('修改密码').fontSize(18).fontWeight(FontWeight.Bold);
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      Column() {
        TextInput({ placeholder: '旧密码', text: this.oldPwd })
          .type(InputType.Password)
          .height(48).margin({ bottom: 12 })
          .onChange((v: string) => { this.oldPwd = v; });
        TextInput({ placeholder: '新密码', text: this.newPwd })
          .type(InputType.Password)
          .height(48).margin({ bottom: 12 })
          .onChange((v: string) => { this.newPwd = v; });
        TextInput({ placeholder: '确认新密码', text: this.confirmPwd })
          .type(InputType.Password)
          .height(48).margin({ bottom: 24 })
          .onChange((v: string) => { this.confirmPwd = v; });

        Button(this.saving ? '提交中...' : '提交')
          .type(ButtonType.Capsule)
          .width('100%').height(48)
          .backgroundColor('#007AFF')
          .enabled(!this.saving)
          .onClick(() => { this.submit(); });
        if (this.resultMsg.length > 0) {
          Text(this.resultMsg)
            .fontSize(13)
            .fontColor(this.isError ? '#FF3B30' : '#34C759')
            .margin({ top: 12 });
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

export default ChangePasswordPage;
