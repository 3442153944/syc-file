import router from '@ohos.router';
import { SyncApi } from '../api/sync/SyncApi';
import { deviceInfoSvc } from '../util/DeviceInfo';
import type { SyncTaskInfo, SyncConflictInfo } from '../api/sync/SyncTypes';
import { wsManager, WsState } from '../net/WebSocketManager';

/**
 * SyncListPage
 * 同步列表页：两 tab——同步记录（全部任务）/ 待处理（本设备 pending + 冲突列表）。
 * 对标 Android ui/screen/sync/SyncListScreen.kt 简化版。
 */
@Entry
@Component
struct SyncListPage {
  @State currentTab: number = 0;
  @State tasks: SyncTaskInfo[] = [];
  @State pending: SyncTaskInfo[] = [];
  @State conflicts: SyncConflictInfo[] = [];
  @State wsState: WsState = WsState.DISCONNECTED;
  @State loading: boolean = false;
  @State errorMsg: string = '';

  aboutToAppear(): void {
    this.refresh();
  }

  async refresh(): Promise<void> {
    this.loading = true;
    this.errorMsg = '';
    try {
      const deviceId: string = deviceInfoSvc.getDeviceId();
      const tResp = await SyncApi.listTasks('', '', 100);
      this.tasks = tResp.data !== null ? tResp.data : [];
      const pResp = await SyncApi.pendingTasks(deviceId);
      this.pending = pResp.data !== null ? pResp.data : [];
      const cResp = await SyncApi.listConflicts();
      this.conflicts = cResp.data !== null ? cResp.data : [];
      this.wsState = wsManager.getState();
    } catch (e) {
      this.errorMsg = e instanceof Error ? e.message : JSON.stringify(e);
    } finally {
      this.loading = false;
    }
  }

  async resolveConflict(id: number, resolution: string): Promise<void> {
    try {
      await SyncApi.resolveConflict(id, resolution);
      await this.refresh();
    } catch (e) {
      console.error(`resolve conflict failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  async deleteConflict(id: number): Promise<void> {
    try {
      await SyncApi.deleteConflict(id);
      await this.refresh();
    } catch (e) {
      console.error(`delete conflict failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('同步列表').fontSize(18).fontWeight(FontWeight.Bold);
        Blank();
        Text('↻').fontSize(22).padding({ right: 12 })
          .onClick(() => { this.refresh(); });
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      // WS 状态徽章
      Row() {
        Text('WS:').fontSize(12).fontColor('#9E9E9E');
        Text(this.wsStateLabel()).fontSize(12)
          .fontColor(this.wsState === WsState.CONNECTED ? '#34C759' : '#9E9E9E')
          .margin({ left: 4 });
      }.width('100%').padding({ left: 16, bottom: 8 });

      Tabs({ barPosition: BarPosition.Start, index: this.currentTab }) {
        TabContent() {
          this.taskListSection(this.tasks);
        }
        .tabBar(this.tabBarItem('同步记录', 0));

        TabContent() {
          Column() {
            this.taskListSection(this.pending);
            if (this.conflicts.length > 0) {
              Text('冲突待办').fontSize(13).fontColor('#9E9E9E')
                .padding({ left: 16, top: 12, bottom: 8 });
              List({ space: 8 }) {
                ForEach(this.conflicts, (c: SyncConflictInfo, idx: number) => {
                  ListItem() {
                    this.conflictCard(c);
                  }
                }, (c: SyncConflictInfo, idx: number) => c.id.toString());
              }
              .width('92%')
              .backgroundColor('#FFFFFF')
              .borderRadius(12);
            }
          }.height('100%');
        }
        .tabBar(this.tabBarItem('待处理', 1));
      }
      .barMode(BarMode.Fixed)
      .onChange((idx: number) => { this.currentTab = idx; })
      .layoutWeight(1);

      if (this.loading) {
        LoadingProgress().width(28).height(28);
      }
      if (this.errorMsg.length > 0) {
        Text(this.errorMsg).fontSize(12).fontColor('#FF3B30').padding(8);
      }
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
  tabBarItem(label: string, idx: number) {
    Text(label).fontSize(14)
      .fontColor(this.currentTab === idx ? '#007AFF' : '#9E9E9E')
      .padding({ top: 8, bottom: 8 });
  }

  @Builder
  taskListSection(list: SyncTaskInfo[]) {
    if (list.length === 0) {
      Column() {
        Text('无任务').fontSize(14).fontColor('#9E9E9E');
      }.width('100%').justifyContent(FlexAlign.Center).alignItems(HorizontalAlign.Center).margin({ top: 32 });
    } else {
      List({ space: 8 }) {
        ForEach(list, (t: SyncTaskInfo, idx: number) => {
          ListItem() {
            this.taskCard(t);
          }
        }, (t: SyncTaskInfo, idx: number) => t.id.toString());
      }
      .width('92%')
      .margin({ top: 8 });
    }
  }

  @Builder
  taskCard(t: SyncTaskInfo) {
    Column() {
      Row() {
        Text(`#${t.id} ${t.task_type}`).fontSize(13).fontWeight(FontWeight.Medium);
        Blank();
        Text(this.statusLabel(t.sync_status)).fontSize(11)
          .fontColor(this.statusColor(t.sync_status));
      }.width('100%');
      Text(`${t.relative_path}/${t.file_name}`).fontSize(12).fontColor('#666666').margin({ top: 4 }).maxLines(2);
      Row() {
        Text(`类型: ${t.task_type}`).fontSize(11).fontColor('#9E9E9E');
        Text(`  方向: ${t.direction}`).fontSize(11).fontColor('#9E9E9E');
        Blank();
        Text(this.formatTime(t.created_at)).fontSize(11).fontColor('#9E9E9E');
      }.width('100%').margin({ top: 4 });
      if (t.error_message !== null && t.error_message !== undefined && t.error_message.length > 0) {
        Text(`错误: ${t.error_message}`).fontSize(11).fontColor('#FF3B30').margin({ top: 2 });
      }
    }
    .width('100%')
    .padding(12)
    .backgroundColor('#FFFFFF')
    .borderRadius(12);
  }

  @Builder
  conflictCard(c: SyncConflictInfo) {
    Column() {
      Text(`#${c.id} ${c.relative_path}/${c.file_name}`).fontSize(13).maxLines(1);
      Row() {
        Text(`server: ${(c.server_hash ?? '').substring(0, 12)}`).fontSize(11).fontColor('#9E9E9E');
        Blank();
        Text(`local: ${(c.local_hash ?? '').substring(0, 12)}`).fontSize(11).fontColor('#9E9E9E');
      }.width('100%').margin({ top: 4 });
      Row() {
        Button('采用服务端').layoutWeight(1).fontSize(12)
          .onClick(() => { this.resolveConflict(c.id, 'accept_server'); });
        Text('  ').fontSize(12);
        Button('删除').layoutWeight(1).fontSize(12).backgroundColor('#FF3B30')
          .onClick(() => { this.deleteConflict(c.id); });
      }.width('100%').margin({ top: 8 });
    }
    .width('100%')
    .padding(12)
    .backgroundColor('#FFFFFF')
    .borderRadius(12);
  }

  private statusLabel(s: string): string {
    switch (s) {
      case 'pending': return '待处理';
      case 'syncing': return '执行中';
      case 'completed': return '已完成';
      case 'failed': return '失败';
      case 'waiting_unlock': return '等待解锁';
      default: return s;
    }
  }

  private statusColor(s: string): string {
    switch (s) {
      case 'completed': return '#34C759';
      case 'failed': return '#FF3B30';
      case 'syncing': return '#007AFF';
      case 'waiting_unlock': return '#FF9500';
      default: return '#9E9E9E';
    }
  }

  private formatTime(iso: string): string {
    if (iso === null || iso === undefined || iso.length === 0) return '';
    const idx: number = iso.indexOf('T');
    return idx > 0 ? `${iso.substring(0, 10)} ${iso.substring(idx + 1, idx + 9)}` : iso;
  }
}

export default SyncListPage;
