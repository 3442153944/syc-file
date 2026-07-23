import router from '@ohos.router';
import { FileApi } from '../api/file/FileApi';
import { WsApi } from '../api/ws/WsApi';
import type { DiskInfo } from '../api/file/FileTypes';
import { AppRoutes } from '../router/AppRoutes';
import { wsManager, WsState } from '../net/WebSocketManager';
import { appConfig } from '../app/AppConfig';

/**
 * MonitorPage
 * 监控面板：服务器状态 + 在线设备概览 + 存储用量。
 * 对标 Android MonitorScreen.kt。
 */
@Component
export struct MonitorPage {
  @State serverOnline: boolean = false;
  @State wsConnected: boolean = false;
  @State deviceCount: number = 0;
  @State disks: DiskInfo[] = [];
  @State loading: boolean = true;
  private unregWs: (() => void) | null = null;

  aboutToAppear(): void {
    this.wsConnected = wsManager.isConnected();
    this.unregWs = wsManager.onState((state: WsState) => {
      this.wsConnected = state === WsState.CONNECTED;
      if (state === WsState.CONNECTED) {
        this.loadDevices();
      }
    });
    this.loadAll();
  }

  aboutToDisappear(): void {
    if (this.unregWs) {
      this.unregWs();
      this.unregWs = null;
    }
  }

  async loadAll(): Promise<void> {
    this.loading = true;
    await Promise.all([this.pingServer(), this.loadDevices(), this.loadDisks()]);
    this.loading = false;
  }

  async pingServer(): Promise<void> {
    try {
      const resp = await FileApi.getAvailableDisks();
      this.serverOnline = resp.code === 200;
    } catch (e) {
      this.serverOnline = false;
    }
  }

  async loadDevices(): Promise<void> {
    try {
      const resp = await WsApi.getMyDevices();
      if (resp.data !== null && resp.data.connections !== null) {
        this.deviceCount = resp.data.total_count;
      }
    } catch (e) { /* ignore */ }
  }

  async loadDisks(): Promise<void> {
    try {
      const resp = await FileApi.getAvailableDisks();
      if (resp.data !== null) {
        this.disks = resp.data.allowed_disks;
      }
    } catch (e) { /* ignore */ }
  }

  build() {
    Scroll() {
      Column() {
        Text('监控').fontSize(22).fontWeight(FontWeight.Bold)
          .width('100%').margin({ left: 16, top: 16, bottom: 12 });

        // 服务器状态卡片
        Row() {
          Column() {
            Text('服务器状态').fontSize(14).fontColor('#9E9E9E');
            Row() {
              Text('●')
                .fontSize(10)
                .fontColor(this.serverOnline ? '#34C759' : '#FF3B30');
              Text(this.serverOnline ? '在线' : '离线')
                .fontSize(18).fontWeight(FontWeight.Medium).margin({ left: 8 });
            }.margin({ top: 8 });
          }.alignItems(HorizontalAlign.Start).layoutWeight(1);

          Column() {
            Text('WebSocket').fontSize(14).fontColor('#9E9E9E');
            Text(this.wsConnected ? '已连接' : '未连接')
              .fontSize(14).fontWeight(FontWeight.Medium)
              .fontColor(this.wsConnected ? '#34C759' : '#FF3B30')
              .margin({ top: 8 });
          }.alignItems(HorizontalAlign.End);
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12);

        // 服务器地址
        Row() {
          Text('服务器地址').fontSize(14).fontColor('#9E9E9E');
          Blank();
          Text(`${appConfig.server}:${appConfig.port}`).fontSize(14);
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12 });

        // 在线设备卡片（可点击进入设备列表）
        Row() {
          Column() {
            Text('在线设备').fontSize(14).fontColor('#9E9E9E');
            Text(`${this.deviceCount} 台`).fontSize(18).fontWeight(FontWeight.Medium).margin({ top: 8 });
          }.alignItems(HorizontalAlign.Start).layoutWeight(1);
          Text('查看详情 ›').fontSize(13).fontColor('#007AFF');
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12 })
        .onClick(() => { router.pushUrl({ url: AppRoutes.DEVICE_LIST }); });

        // 存储用量
        Column() {
          Text('存储用量').fontSize(14).fontWeight(FontWeight.Medium).margin({ bottom: 12 });
          if (this.disks.length === 0) {
            Text('暂无数据').fontSize(13).fontColor('#9E9E9E');
          } else {
            ForEach(this.disks, (disk: DiskInfo) => {
              Column() {
                Row() {
                  Text(disk.path).fontSize(14).layoutWeight(1);
                  Text(`${disk.used_percent.toFixed(0)}%`).fontSize(13).fontColor('#9E9E9E');
                }.width('100%').margin({ bottom: 4 });
                Progress({ value: Math.floor(disk.used_percent), total: 100, type: ProgressType.Linear })
                  .width('100%').margin({ bottom: 12 });
              }
            }, (disk: DiskInfo) => disk.path);
          }
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12, bottom: 24 });

        // 刷新按钮
        Button('刷新').type(ButtonType.Capsule).width('92%').height(40)
          .backgroundColor('#007AFF').margin({ bottom: 24 })
          .onClick(() => { this.loadAll(); });
      }
      .width('100%')
      .alignItems(HorizontalAlign.Center);
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5')
    .scrollBar(BarState.Off);
  }
}

export default MonitorPage;
