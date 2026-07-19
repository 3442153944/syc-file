import router from '@ohos.router';
import { preferences } from '@kit.ArkData';
import http from '@ohos.net.http';
import { UserApi } from '../api/user/UserApi';
import { userStore } from '../app/UserStore';
import type { LoginParams } from '../api/user/UserTypes';
import { AppRoutes } from '../router/AppRoutes';
import { appConfig } from '../app/AppConfig';

/**
 * LoginPage
 * 登录页：用户名 + 密码 + 记住密码 + 服务器地址快速入口。
 * 对标 Android ui/screen/person/LoginScreen.kt。
 */
@Entry
@Component
struct LoginPage {
  @State username: string = '';
  @State password: string = '';
  @State remember: boolean = true;
  @State loading: boolean = false;
  @State errorMsg: string = '';
  @State showServerDialog: boolean = false;
  @State serverInput: string = '';
  @State portInput: string = '';
  @State routePathInput: string = '';
  @State httpsInput: boolean = true;
  @State testResult: string = '';
  @State testing: boolean = false;

  aboutToAppear(): void {
    this.syncInputsFromConfig();
    this.loadSavedCredentials();
  }

  private syncInputsFromConfig(): void {
    this.serverInput = appConfig.server;
    this.portInput = String(appConfig.port);
    this.routePathInput = appConfig.routePath;
    this.httpsInput = appConfig.isHttps;
  }

  private async loadSavedCredentials(): Promise<void> {
    try {
      const ctx = this.getUIContext().getHostContext() as Context;
      const prefs = await preferences.getPreferences(ctx, 'auth_storage');
      const remember = (await prefs.get('remember_password', false as Object)) as boolean;
      this.remember = remember;
      if (remember) {
        this.username = (await prefs.get('saved_username', '')) as string;
        this.password = (await prefs.get('saved_password', '')) as string;
      }
    } catch (e) { /* ignore */ }
  }

  async doLogin(): Promise<void> {
    this.errorMsg = '';
    if (this.username.length === 0 || this.password.length === 0) {
      this.errorMsg = '请输入用户名和密码';
      return;
    }
    this.loading = true;
    try {
      const params: LoginParams = {
        username: this.username,
        password: this.password,
      };
      const resp = await UserApi.login(params);
      if (resp.data) {
        await userStore.setCurrent(resp.data.user);
        if (this.remember) {
          await this.saveCredentials(this.username, this.password);
        } else {
          await this.clearCredentials();
        }
        router.replaceUrl({ url: AppRoutes.MAIN_SHELL });
      } else {
        this.errorMsg = resp.message || '登录失败';
      }
    } catch (e) {
      this.errorMsg = (e as Error)?.message ?? JSON.stringify(e);
    } finally {
      this.loading = false;
    }
  }

  private async saveCredentials(u: string, p: string): Promise<void> {
    try {
      const ctx = this.getUIContext().getHostContext() as Context;
      const prefs = await preferences.getPreferences(ctx, 'auth_storage');
      await prefs.put('saved_username', u);
      await prefs.put('saved_password', p);
      await prefs.put('remember_password', true as Object);
      await prefs.flush();
    } catch (e) { /* ignore */ }
  }

  private async clearCredentials(): Promise<void> {
    try {
      const ctx = this.getUIContext().getHostContext() as Context;
      const prefs = await preferences.getPreferences(ctx, 'auth_storage');
      await prefs.delete('saved_username');
      await prefs.delete('saved_password');
      await prefs.delete('remember_password');
      await prefs.flush();
    } catch (e) { /* ignore */ }
  }

  async applyServer(): Promise<void> {
    if (this.serverInput.length === 0) {
      return;
    }
    await appConfig.update(
      this.serverInput.trim(),
      this.routePathInput.trim(),
      this.httpsInput,
      Number(this.portInput) || 8443
    );
    this.showServerDialog = false;
  }

  async resetServerConfig(): Promise<void> {
    await appConfig.resetToDefaults();
    this.syncInputsFromConfig();
    this.testResult = '已恢复默认配置';
  }

  async testConnection(): Promise<void> {
    this.testResult = '';
    this.testing = true;
    const srv = this.serverInput.trim();
    const port = Number(this.portInput) || 8891;
    const protocol = this.httpsInput ? 'https' : 'http';
    const url = `${protocol}://${srv}:${port}/ping`;

    const req = http.createHttp();
    try {
      const opts: http.HttpRequestOptions = {
        method: http.RequestMethod.GET,
        connectTimeout: 5000,
        readTimeout: 5000,
        expectDataType: http.HttpDataType.STRING,
      };
      if (appConfig.caPath.length > 0) {
        opts.caPath = appConfig.caPath;
      }
      const start = Date.now();
      const resp = await req.request(url, opts);
      const elapsed = Date.now() - start;
      if (resp.responseCode === 200) {
        this.testResult = `连接成功 (${elapsed}ms)`;
      } else if (resp.responseCode === 404) {
        this.testResult = `服务器可达，但 /ping 返回 404，请检查路由路径 (${elapsed}ms)`;
      } else {
        this.testResult = `HTTP ${resp.responseCode} (${elapsed}ms)`;
      }
    } catch (e) {
      const msg = (e as Error)?.message ?? JSON.stringify(e);
      if (msg.includes('Unable to resolve host') || msg.includes('getaddrinfo')) {
        this.testResult = '域名解析失败，请检查服务器地址';
      } else if (msg.includes('Connection refused') || msg.includes('ECONNREFUSED')) {
        this.testResult = '连接被拒绝，请检查端口是否正确';
      } else if (msg.includes('timeout') || msg.includes('ETIMEDOUT')) {
        this.testResult = '连接超时，请检查网络或服务器状态';
      } else if (msg.includes('TLS') || msg.includes('certificate') || msg.includes('ssl')) {
        this.testResult = 'TLS 握手失败，服务器可能不支持 HTTPS，请尝试关闭 HTTPS 开关';
      } else {
        this.testResult = `连接失败: ${msg}`;
      }
    } finally {
      req.destroy();
      this.testing = false;
    }
  }

  build() {
    Stack({ alignContent: Alignment.Center }) {
      Column() {
        // 顶部标题 + 服务器设置入口
        Row() {
          Text('云梯 FileSync')
            .fontSize(28)
            .fontWeight(FontWeight.Bold)
            .margin({ left: 16, top: 32, bottom: 8 });
          Blank();
          Text('⚙').fontSize(22)
            .margin({ right: 16, top: 32 })
            .onClick(() => { this.showServerDialog = !this.showServerDialog; });
        }.width('100%');

        Column() {
          TextInput({ placeholder: '用户名', text: this.username })
            .onChange((v: string) => { this.username = v; })
            .height(48).margin({ bottom: 12 })
            .width('100%');

          TextInput({ placeholder: '密码', text: this.password })
            .type(InputType.Password)
            .onChange((v: string) => { this.password = v; })
            .height(48).margin({ bottom: 12 })
            .width('100%');

          Row() {
            Toggle({ type: ToggleType.Checkbox, isOn: this.remember })
              .onChange((v: boolean) => { this.remember = v; });
            Text('记住密码').fontSize(14).margin({ left: 8 });
            Blank();
            Text('忘记密码？').fontSize(14).fontColor('#007AFF')
              .onClick(() => { /* TODO: 跳转重置密码页 */ });
          }.width('100%').margin({ bottom: 24 });

          if (this.errorMsg.length > 0) {
            Text(this.errorMsg)
              .fontSize(13).fontColor('#FF3B30')
              .margin({ bottom: 12 });
          }

          Button(this.loading ? '登录中...' : '登录')
            .type(ButtonType.Capsule)
            .width('100%').height(48)
            .backgroundColor('#007AFF')
            .enabled(!this.loading)
            .onClick(() => { this.doLogin(); });
        }
        .width('90%')
        .padding(20)
        .backgroundColor('#FFFFFF')
        .borderRadius(12)
        .margin({ top: 24 });
      }
      .width('100%')
      .height('100%')
      .backgroundColor('#F5F5F5')
      .alignItems(HorizontalAlign.Center);

      if (this.showServerDialog) {
        this.serverDialogBuilder();
      }
    }
    .width('100%')
    .height('100%');
  }

  @Builder
  serverDialogBuilder() {
    Column() {
      Column() {
        Text('服务器地址').fontSize(18).fontWeight(FontWeight.Medium).margin({ bottom: 16 });
        TextInput({ placeholder: '域名/IP', text: this.serverInput })
          .onChange((v: string) => { this.serverInput = v; })
          .height(40).margin({ bottom: 8 });
        TextInput({ placeholder: '端口', text: this.portInput })
          .type(InputType.Number)
          .onChange((v: string) => { this.portInput = v; })
          .height(40).margin({ bottom: 8 });
        TextInput({ placeholder: '路径前缀（如 /file，可空）', text: this.routePathInput })
          .onChange((v: string) => { this.routePathInput = v; })
          .height(40).margin({ bottom: 8 });
        Row() {
          Text('HTTPS').fontSize(14);
          Toggle({ type: ToggleType.Switch, isOn: this.httpsInput })
            .onChange((v: boolean) => { this.httpsInput = v; });
          Blank();
        }.width('100%').margin({ bottom: 16 });

        Button(this.testing ? '测试中...' : '测试连接')
          .type(ButtonType.Capsule)
          .width('100%').height(40)
          .backgroundColor('#E5E5EA')
          .fontColor('#000000')
          .enabled(!this.testing && this.serverInput.trim().length > 0)
          .onClick(() => { this.testConnection(); });

        if (this.testResult.length > 0) {
          Text(this.testResult)
            .fontSize(13)
            .fontColor((this.testResult.includes('成功') || this.testResult.includes('已恢复')) ? '#34C759' : '#FF3B30')
            .margin({ top: 8, bottom: 4 });
        }

        Button('恢复默认设置')
          .type(ButtonType.Capsule)
          .width('100%').height(40)
          .backgroundColor('#FFF3F3')
          .fontColor('#FF3B30')
          .margin({ top: 4, bottom: 8 })
          .onClick(() => { this.resetServerConfig(); });

        Row() {
          Button('取消').layoutWeight(1)
            .onClick(() => { this.showServerDialog = false; });
          Button('保存').layoutWeight(1).backgroundColor('#007AFF')
            .onClick(() => { this.applyServer(); });
        }.width('100%');
      }
      .width('80%')
      .padding(20)
      .backgroundColor('#FFFFFF')
      .borderRadius(12);
    }
    .width('100%')
    .height('100%')
    .backgroundColor('rgba(0,0,0,0.5)')
    .justifyContent(FlexAlign.Center)
    .alignItems(HorizontalAlign.Center);
  }
}

export default LoginPage;
