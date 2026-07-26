import router from '@ohos.router';
import { FileApi } from '../api/file/FileApi';
import { WsApi } from '../api/ws/WsApi';
import type { DiskInfo, DownloadHistoryItem } from '../api/file/FileTypes';
import { AppRoutes } from '../router/AppRoutes';
import { wsManager, WsState } from '../net/WebSocketManager';
import { USER_STORAGE_KEY } from '../app/UserStore';
import { STAGED_PENDING_KEY } from '../storage/SyncStagingStore';
import { syncEngine } from '../sync/SyncEngine';
import { FLUSH_LOG_KEY, FLUSH_ACTIVE_KEY, FlushEventKind, formatClock } from '../storage/FlushLog';
import type { FlushEvent } from '../storage/FlushLog';
import type { UserInfo } from '../api/user/UserTypes';

/**
 * HomePage
 * 仪表盘：存储用量 / 在线设备数 / 同步状态 / 最近下载 / 快捷入口。
 * 对标 Android HomeScreen.kt。
 */
@Component
export struct HomePage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;
  /** 后台同步落在沙盒、等回前台归位到共享目录的条目数（见 storage/SyncStagingStore）。 */
  @StorageLink(STAGED_PENDING_KEY) stagedPending: number = 0;
  /** 搬运流水（新的在前），由 storage/FlushLog 推到 AppStorage。 */
  @StorageLink(FLUSH_LOG_KEY) @Watch('onFeedChanged') flushLogItems: FlushEvent[] = [];
  /** 正在搬运时按「消息流」展示。 */
  @StorageLink(FLUSH_ACTIVE_KEY) flushActive: boolean = false;
  private feedScroller: Scroller = new Scroller();
  @State totalBytes: number = 0;
  @State usedBytes: number = 0;
  @State deviceCount: number = 0;
  @State wsConnected: boolean = false;
  @State recentDownloads: DownloadHistoryItem[] = [];
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
    // 前台可见时沙盒里不该有东西，进首页就催一次归位
    syncEngine.kickFlush();
  }

  aboutToDisappear(): void {
    if (this.unregWs) {
      this.unregWs();
      this.unregWs = null;
    }
  }

  async loadAll(): Promise<void> {
    this.loading = true;
    await Promise.all([this.loadStorage(), this.loadDevices(), this.loadRecentDownloads()]);
    this.loading = false;
  }

  async loadStorage(): Promise<void> {
    try {
      const resp = await FileApi.getAvailableDisks();
      if (resp.data !== null) {
        let total = 0;
        let used = 0;
        const disks: DiskInfo[] = resp.data.allowed_disks;
        for (let i = 0; i < disks.length; i++) {
          total += disks[i].total;
          used += disks[i].used;
        }
        this.totalBytes = total;
        this.usedBytes = used;
      }
    } catch (e) { /* ignore */ }
  }

  async loadDevices(): Promise<void> {
    try {
      const resp = await WsApi.getMyDevices();
      if (resp.data !== null && resp.data.connections !== null) {
        this.deviceCount = resp.data.total_count;
      }
    } catch (e) { /* ignore */ }
  }

  async loadRecentDownloads(): Promise<void> {
    try {
      const resp = await FileApi.getDownloadHistory({ pageNum: 1, pageSize: 5 });
      if (resp.data !== null) {
        this.recentDownloads = resp.data.list;
      }
    } catch (e) { /* ignore */ }
  }

  /**
   * 搬运中来新消息时自动滚回最新一条（新的在列表最前），做出「消息流」的感觉。
   * 空闲时不抢滚动位置，让用户能安静地翻历史。
   */
  onFeedChanged(): void {
    if (!this.flushActive) return;
    try {
      this.feedScroller.scrollToIndex(0);
    } catch (e) { /* 列表还没布局好时忽略 */ }
  }

  /**
   * 文件归位消息流。
   *
   * 搬运中 → 实时消息流（新的自动滚到最上面）；空闲 → 可翻的搬运历史。
   * 沙盒是中转不是存储，这里既是进度反馈，也是出问题时唯一能看清「卡在哪一步」的地方。
   */
  @Builder
  flushFeed() {
    Column() {
      Row() {
        Text('文件归位').fontSize(14).fontWeight(FontWeight.Medium);
        if (this.flushActive) {
          Text('搬运中')
            .fontSize(10).fontColor('#FFFFFF')
            .backgroundColor('#FF9500').borderRadius(8)
            .padding({ left: 6, right: 6, top: 2, bottom: 2 })
            .margin({ left: 8 });
        }
        Blank();
        if (this.stagedPending > 0) {
          Text(`待归位 ${this.stagedPending} · 点击立即搬运`)
            .fontSize(11).fontColor('#FF9500');
        } else {
          Text('沙盒已清空').fontSize(11).fontColor('#34C759');
        }
      }
      .width('100%')
      .onClick(() => { syncEngine.kickFlush(); });

      if (this.flushLogItems.length === 0) {
        Text('暂无搬运记录').fontSize(13).fontColor('#9E9E9E').margin({ top: 12 });
      } else {
        List({ space: 6, scroller: this.feedScroller }) {
          ForEach(this.flushLogItems, (item: FlushEvent) => {
            ListItem() {
              Row() {
                Text(this.kindIcon(item.kind)).fontSize(12).width(18);
                Text(item.fileName)
                  .fontSize(12).layoutWeight(1)
                  .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
                Text(this.kindLabel(item.kind))
                  .fontSize(11).fontColor(this.kindColor(item.kind)).margin({ left: 6 });
                Text(formatClock(item.at))
                  .fontSize(10).fontColor('#BDBDBD').margin({ left: 6 });
              }.width('100%').alignItems(VerticalAlign.Center);
            }
          }, (item: FlushEvent) => item.id.toString());
        }
        .width('100%')
        .height(132)
        .margin({ top: 8 })
        .scrollBar(BarState.Auto)
        .edgeEffect(EdgeEffect.Spring);
      }
    }
    .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12 })
    .alignItems(HorizontalAlign.Start);
  }

  private kindIcon(kind: string): string {
    if (kind === FlushEventKind.MOVED as string) return '✅';
    if (kind === FlushEventKind.FAILED as string) return '⚠️';
    if (kind === FlushEventKind.SCAN as string) return '📋';
    return '📦';
  }

  private kindLabel(kind: string): string {
    if (kind === FlushEventKind.MOVED as string) return '已归位';
    if (kind === FlushEventKind.FAILED as string) return '失败·将重试';
    if (kind === FlushEventKind.SCAN as string) return '清单';
    return '暂存中';
  }

  private kindColor(kind: string): string {
    if (kind === FlushEventKind.MOVED as string) return '#34C759';
    if (kind === FlushEventKind.FAILED as string) return '#FF3B30';
    if (kind === FlushEventKind.SCAN as string) return '#007AFF';
    return '#FF9500';
  }

  private formatBytes(bytes: number): string {
    if (bytes >= 1099511627776) return `${(bytes / 1099511627776).toFixed(1)} TB`;
    if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
  }

  build() {
    Scroll() {
      Column() {
        // 顶栏
        Row() {
          Text('云梯').fontSize(24).fontWeight(FontWeight.Bold);
          Blank();
          Text(this.wsConnected ? '已连接' : '未连接')
            .fontSize(12)
            .fontColor(this.wsConnected ? '#34C759' : '#FF3B30')
            .padding({ left: 8, right: 8, top: 3, bottom: 3 })
            .borderRadius(10)
            .backgroundColor(this.wsConnected ? '#E8F8EE' : '#FFEBEE');
        }.width('100%').padding({ left: 16, right: 16, top: 16, bottom: 8 });

        // 用户问候
        Text(`你好，${this.currentUser?.username ?? '用户'}`)
          .fontSize(16).fontColor('#666666').margin({ left: 16, top: 12, bottom: 12 });

        // 存储卡片
        Column() {
          Text('存储空间').fontSize(14).fontWeight(FontWeight.Medium);
          Row() {
            Text(this.formatBytes(this.usedBytes)).fontSize(22).fontWeight(FontWeight.Bold);
            Text(` / ${this.formatBytes(this.totalBytes)}`).fontSize(14).fontColor('#9E9E9E');
          }.margin({ top: 8 });
          Progress({
            value: this.totalBytes > 0 ? Math.floor((this.usedBytes / this.totalBytes) * 100) : 0,
            total: 100,
            type: ProgressType.Linear
          }).width('100%').margin({ top: 8 });
        }
        .width('92%').padding(16).backgroundColor($r('sys.color.comp_background_list_card')).borderRadius(12).margin({ top: 4 });

        // 设备 + 同步状态卡片
        Row() {
          Column() {
            Text(`${this.deviceCount}`).fontSize(28).fontWeight(FontWeight.Bold).fontColor('#007AFF');
            Text('在线设备').fontSize(12).fontColor('#9E9E9E').margin({ top: 4 });
          }
          .layoutWeight(1).alignItems(HorizontalAlign.Center)
          .onClick(() => { router.pushUrl({ url: AppRoutes.DEVICE_LIST }); });

          Divider().vertical(true).height(40).color('#F0F0F0');

          Column() {
            Text(this.wsConnected ? '运行中' : '离线')
              .fontSize(16).fontWeight(FontWeight.Medium)
              .fontColor(this.wsConnected ? '#34C759' : '#FF3B30');
            Text('同步状态').fontSize(12).fontColor('#9E9E9E').margin({ top: 4 });
          }.layoutWeight(1).alignItems(HorizontalAlign.Center);
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12 });

        // 快捷操作
        Row() {
          Column() {
            Text('⬆').fontSize(20);
            Text('上传').fontSize(12).margin({ top: 4 });
          }
          .layoutWeight(1).alignItems(HorizontalAlign.Center)
          .onClick(() => { router.pushUrl({ url: AppRoutes.UPLOAD }); });

          Column() {
            Text('📁').fontSize(20);
            Text('文件').fontSize(12).margin({ top: 4 });
          }
          .layoutWeight(1).alignItems(HorizontalAlign.Center)
          .onClick(() => { router.pushUrl({ url: AppRoutes.FILE_BROWSE, params: { 'path': 'D:/' } }); });

          Column() {
            Text('🔄').fontSize(20);
            Text('同步').fontSize(12).margin({ top: 4 });
          }
          .layoutWeight(1).alignItems(HorizontalAlign.Center)
          .onClick(() => { router.pushUrl({ url: AppRoutes.SYNC_LIST }); });

          Column() {
            Text('📋').fontSize(20);
            Text('传输').fontSize(12).margin({ top: 4 });
          }
          .layoutWeight(1).alignItems(HorizontalAlign.Center)
          .onClick(() => { router.pushUrl({ url: AppRoutes.TRANSFERS }); });
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12 });

        // 文件归位（沙盒 → 共享目录）滚动消息流 / 历史
        this.flushFeed();

        // 最近下载
        Column() {
          Row() {
            Text('最近下载').fontSize(14).fontWeight(FontWeight.Medium);
            Blank();
            Text('查看全部').fontSize(12).fontColor('#007AFF')
              .onClick(() => { router.pushUrl({ url: AppRoutes.TRANSFERS }); });
          }.width('100%');

          if (this.recentDownloads.length === 0) {
            Text('暂无下载记录').fontSize(13).fontColor('#9E9E9E').margin({ top: 12 });
          } else {
            ForEach(this.recentDownloads, (item: DownloadHistoryItem) => {
              Row() {
                Column() {
                  Text(item.file_name ?? '未知文件').fontSize(14).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
                  Text(item.created_at ?? '').fontSize(11).fontColor('#9E9E9E').margin({ top: 2 });
                }.alignItems(HorizontalAlign.Start).layoutWeight(1);
                Text(item.download_status === 'completed' ? '✓' : '…')
                  .fontSize(14)
                  .fontColor(item.download_status === 'completed' ? '#34C759' : '#FF9500');
              }
              .width('100%').padding({ top: 10, bottom: 10 })
              .border({ width: { bottom: 1 }, color: '#F5F5F5' });
            }, (item: DownloadHistoryItem) => item.id.toString());
          }
        }
        .width('92%').padding(16).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 12, bottom: 24 });
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

export default HomePage;
