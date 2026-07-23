import router from '@ohos.router';
import { appConfig } from '../app/AppConfig';
import { wsManager, WsState } from '../net/WebSocketManager';
import { backgroundTaskHelper } from '../service/BackgroundTaskHelper';

/**
 * SettingsPage
 * 设置页：同步开关、保活（长时任务）、WiFi 限制、分片大小、WS 状态。
 * 对标 Android SettingsScreen 同步设置部分。
 */
@Entry
@Component
struct SettingsPage {
  @State autoSync: boolean = appConfig.autoSyncEnabled;
  @State forceKeep: boolean = appConfig.forceKeepAliveEnabled;
  @State wifiOnly: boolean = appConfig.syncOnWifiOnly;
  @State chunkSize: number = appConfig.uploadChunkSize;
  @State wsState: WsState = wsManager.getState();
  @State keepAliveRunning: boolean = backgroundTaskHelper.isRunning();

  aboutToAppear(): void {
    this.keepAliveRunning = backgroundTaskHelper.isRunning();
  }

  async toggleAutoSync(v: boolean): Promise<void> {
    this.autoSync = v;
    await appConfig.update(undefined, undefined, undefined, undefined, v);
  }

  async toggleKeep(v: boolean): Promise<void> {
    this.forceKeep = v;
    await appConfig.update(undefined, undefined, undefined, undefined, undefined, undefined, v);
    if (v) {
      await backgroundTaskHelper.start();
    } else {
      await backgroundTaskHelper.stop();
    }
    this.keepAliveRunning = backgroundTaskHelper.isRunning();
  }

  async toggleWifi(v: boolean): Promise<void> {
    this.wifiOnly = v;
    await appConfig.update(undefined, undefined, undefined, undefined, undefined, v);
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('设置').fontSize(18).fontWeight(FontWeight.Bold);
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      // 同步设置
      Text('同步').fontSize(13).fontColor('#9E9E9E').width('92%').margin({ top: 12, bottom: 4 });
      Column() {
        this.toggleRow('自动同步', this.autoSync, (v: boolean) => { this.toggleAutoSync(v); });
        this.toggleRow('仅 WiFi 下同步', this.wifiOnly, (v: boolean) => { this.toggleWifi(v); });
      }
      .width('92%')
      .backgroundColor('#FFFFFF')
      .borderRadius(12);

      // 保活设置
      Text('后台保活').fontSize(13).fontColor('#9E9E9E').width('92%').margin({ top: 16, bottom: 4 });
      Column() {
        this.toggleRow('强制保活（长时任务 + 通知栏常驻）', this.forceKeep, (v: boolean) => { this.toggleKeep(v); });
        this.infoRow('保活状态', this.keepAliveRunning ? '运行中' : '未启动');
      }
      .width('92%')
      .backgroundColor('#FFFFFF')
      .borderRadius(12);

      // 传输设置
      Text('传输').fontSize(13).fontColor('#9E9E9E').width('92%').margin({ top: 16, bottom: 4 });
      Column() {
        this.infoRow('分片大小', `${(this.chunkSize / 1024 / 1024).toFixed(0)} MB`);
        this.infoRow('WS 状态', this.wsStateLabel());
        this.infoRow('应用版本', appConfig.versionName);
      }
      .width('92%')
      .backgroundColor('#FFFFFF')
      .borderRadius(12);
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }

  private wsStateLabel(): string {
    switch (this.wsState) {
      case WsState.CONNECTED: return '已连接';
      case WsState.CONNECTING: return '连接中';
      case WsState.ERROR: return '错误';
      default: return '未连接';
    }
  }

  @Builder
  toggleRow(label: string, isOn: boolean, onChange: (v: boolean) => void) {
    Row() {
      Text(label).fontSize(14).layoutWeight(1);
      Toggle({ type: ToggleType.Switch, isOn: isOn })
        .selectedColor('#34C759')
        .onChange((v: boolean) => { onChange(v); });
    }
    .width('100%')
    .padding({ left: 16, right: 16, top: 14, bottom: 14 })
    .border({ width: { bottom: 1 }, color: '#F0F0F0' });
  }

  @Builder
  infoRow(label: string, value: string) {
    Row() {
      Text(label).fontSize(14);
      Blank();
      Text(value).fontSize(13).fontColor('#9E9E9E');
    }
    .width('100%')
    .padding({ left: 16, right: 16, top: 14, bottom: 14 })
    .border({ width: { bottom: 1 }, color: '#F0F0F0' });
  }
}

export default SettingsPage;
