import router from '@ohos.router';
import { WsApi } from '../api/ws/WsApi';
import type { ConnectionInfo, OnlineUserInfo } from '../api/ws/WsTypes';
import { userStore } from '../app/UserStore';
import { wsManager, WsState } from '../net/WebSocketManager';

/**
 * DeviceListPage
 * 设备列表页：「我的在线设备」（所有人）+「所有在线设备」（仅 admin）。
 * 对标 Android ui/screen/monitoring/DevicesList.kt + DeviceMonitorViewModel.kt。
 */
@Entry
@Component
struct DeviceListPage {
  @State myDevices: ConnectionInfo[] = [];
  @State allUsers: OnlineUserInfo[] = [];
  @State isAdmin: boolean = false;
  @State loading: boolean = true;
  @State errorMsg: string = '';
  private unregWs: (() => void) | null = null;

  aboutToAppear(): void {
    this.isAdmin = userStore.isAdmin();
    this.unregWs = wsManager.onState((state: WsState) => {
      if (state === WsState.CONNECTED) {
        this.refresh();
      }
    });
    this.refresh();
  }

  aboutToDisappear(): void {
    if (this.unregWs) {
      this.unregWs();
      this.unregWs = null;
    }
  }

  async refresh(): Promise<void> {
    this.loading = true;
    this.errorMsg = '';
    try {
      const myResp = await WsApi.getMyDevices();
      if (myResp.data !== null && myResp.data.connections !== null) {
        this.myDevices = myResp.data.connections;
      } else {
        this.myDevices = [];
      }
      if (this.isAdmin) {
        try {
          const allResp = await WsApi.getOnlineUsers();
          if (allResp.data !== null && allResp.data.users !== null) {
            this.allUsers = allResp.data.users;
          } else {
            this.allUsers = [];
          }
        } catch (e) {
          this.allUsers = [];
        }
      }
    } catch (e) {
      this.errorMsg = (e as Error)?.message ?? JSON.stringify(e);
    } finally {
      this.loading = false;
    }
  }

  private platformLabel(dev: ConnectionInfo): string {
    const p = (dev.device?.platform ?? '').toLowerCase();
    if (p === 'android') return 'Android';
    if (p === 'harmony' || p === 'harmonyos') return '鸿蒙';
    if (p === 'pc' || p === 'windows' || p === 'desktop') return 'PC';
    if (p === 'web') return 'Web';
    if (p === 'ios') return 'iOS';
    return p.length > 0 ? dev.device!.platform : '未知';
  }

  private platformIcon(dev: ConnectionInfo): string {
    const p = (dev.device?.platform ?? '').toLowerCase();
    if (p === 'android') return '📱';
    if (p === 'harmony' || p === 'harmonyos') return '📱';
    if (p === 'pc' || p === 'windows' || p === 'desktop') return '💻';
    if (p === 'web') return '🌐';
    if (p === 'ios') return '📱';
    return '📟';
  }

  private deviceName(dev: ConnectionInfo): string {
    const name = dev.device?.device_name ?? '';
    return name.length > 0 ? name : '未知设备';
  }

  private appVersion(dev: ConnectionInfo): string {
    return dev.device?.app_version ?? '';
  }

  private formatDuration(connectedAt: string): string {
    if (!connectedAt || connectedAt.length === 0) return '';
    try {
      const ts = new Date(connectedAt).getTime();
      const diffMs = Date.now() - ts;
      if (diffMs < 0) return '';
      const mins = Math.floor(diffMs / 60000);
      if (mins < 1) return '刚刚';
      if (mins < 60) return `${mins} 分钟`;
      const hours = Math.floor(mins / 60);
      if (hours < 24) return `${hours} 小时 ${mins % 60} 分`;
      const days = Math.floor(hours / 24);
      return `${days} 天 ${hours % 24} 小时`;
    } catch (e) {
      return '';
    }
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('在线设备').fontSize(18).fontWeight(FontWeight.Bold);
        Blank();
        Text('刷新').fontSize(14).fontColor('#007AFF').margin({ right: 16 })
          .onClick(() => { this.refresh(); });
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      if (this.loading) {
        Column() {
          LoadingProgress().width(32).height(32);
          Text('加载中...').fontSize(13).fontColor('#9E9E9E').margin({ top: 8 });
        }.width('100%').layoutWeight(1).justifyContent(FlexAlign.Center);
      } else if (this.errorMsg.length > 0) {
        Column() {
          Text('加载失败').fontSize(14).fontColor('#FF3B30');
          Text(this.errorMsg).fontSize(12).fontColor('#9E9E9E').margin({ top: 4 });
          Button('重试').type(ButtonType.Capsule).margin({ top: 12 })
            .onClick(() => { this.refresh(); });
        }.width('100%').layoutWeight(1).justifyContent(FlexAlign.Center).alignItems(HorizontalAlign.Center);
      } else {
        Scroll() {
          Column() {
            Text('我的在线设备').fontSize(14).fontWeight(FontWeight.Medium)
              .width('92%').margin({ top: 12, bottom: 8 });
            if (this.myDevices.length === 0) {
              Text('暂无在线设备').fontSize(13).fontColor('#9E9E9E')
                .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12);
            } else {
              ForEach(this.myDevices, (dev: ConnectionInfo, idx: number) => {
                Column() {
                  this.deviceCard(dev, '');
                }
              }, (dev: ConnectionInfo, idx: number) => `my-${idx}`);
            }

            if (this.isAdmin && this.allUsers.length > 0) {
              Text('所有在线设备').fontSize(14).fontWeight(FontWeight.Medium)
                .width('92%').margin({ top: 20, bottom: 8 });
              ForEach(this.allUsers, (user: OnlineUserInfo, idx: number) => {
                Column() {
                  ForEach(user.connections, (dev: ConnectionInfo, j: number) => {
                    Column() {
                      this.deviceCard(dev, user.username);
                    }
                  }, (dev: ConnectionInfo, j: number) => `all-${idx}-${j}`);
                }
              }, (user: OnlineUserInfo, idx: number) => `user-${idx}`);
            }
          }
          .width('100%')
          .alignItems(HorizontalAlign.Center)
          .padding({ bottom: 24 });
        }
        .width('100%')
        .layoutWeight(1)
        .scrollBar(BarState.Off);
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }

  @Builder
  deviceCard(dev: ConnectionInfo, ownerName: string) {
    Row() {
      Text(this.platformIcon(dev)).fontSize(28);
      Column() {
        Row() {
          Text(this.deviceName(dev)).fontSize(15).fontWeight(FontWeight.Medium);
          if (ownerName.length > 0) {
            Text(ownerName).fontSize(11).fontColor('#FF9500')
              .padding({ left: 4, right: 4, top: 1, bottom: 1 })
              .border({ width: 1, color: '#FF9500' }).borderRadius(3).margin({ left: 6 });
          }
        }
        Row() {
          Text(this.platformLabel(dev)).fontSize(12).fontColor('#007AFF')
            .padding({ left: 4, right: 4, top: 1, bottom: 1 })
            .backgroundColor('#E8F4FD').borderRadius(3);
          if (dev.ip.length > 0) {
            Text(dev.ip).fontSize(12).fontColor('#9E9E9E').margin({ left: 8 });
          }
        }.margin({ top: 4 });
        Row() {
          if (this.appVersion(dev).length > 0) {
            Text(`v${this.appVersion(dev)}`).fontSize(11).fontColor('#9E9E9E');
          }
          if (this.formatDuration(dev.connected_at).length > 0) {
            Text(`在线 ${this.formatDuration(dev.connected_at)}`).fontSize(11).fontColor('#9E9E9E')
              .margin({ left: 8 });
          }
        }.margin({ top: 2 });
      }.alignItems(HorizontalAlign.Start).margin({ left: 12 }).layoutWeight(1);
    }
    .width('92%')
    .padding(14)
    .backgroundColor('#FFFFFF')
    .borderRadius(12)
    .margin({ bottom: 8 });
  }
}

export default DeviceListPage;
