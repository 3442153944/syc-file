import router from '@ohos.router';
import { downloadController, DOWNLOADS_STORAGE_KEY, DownloadStatus } from '../download/DownloadController';
import type { DownloadItem } from '../download/DownloadController';

/**
 * TransfersPage
 * 传输列表页：显示所有下载项 + 进度 + 暂停/恢复/取消。
 * 对标 Android ui/screen/transmission/FileTransferListScreen.kt 简化版（仅下载部分）。
 */
@Entry
@Component
struct TransfersPage {
  @StorageLink(DOWNLOADS_STORAGE_KEY) items: DownloadItem[] = [];

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('传输列表').fontSize(18).fontWeight(FontWeight.Bold);
        Blank();
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      if (this.items.length === 0) {
        Column() {
          Text('暂无传输任务').fontSize(14).fontColor('#9E9E9E');
        }
        .width('100%')
        .height('100%')
        .justifyContent(FlexAlign.Center)
        .alignItems(HorizontalAlign.Center);
      } else {
        List({ space: 8 }) {
          ForEach(this.items, (item: DownloadItem, idx: number) => {
            ListItem() {
              this.itemCard(item);
            }
          }, (item: DownloadItem, idx: number) => item.id.toString());
        }
        .width('92%')
        .margin({ top: 8 })
        .layoutWeight(1);
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }

  @Builder
  itemCard(item: DownloadItem) {
    Column() {
      Row() {
        Text(item.fileName).fontSize(14).maxLines(1).layoutWeight(1);
        Text(this.statusLabel(item.status)).fontSize(12).fontColor(this.statusColor(item.status));
      }.width('100%');

      Progress({ value: item.progress, total: 100, type: ProgressType.Linear })
        .width('100%')
        .margin({ top: 8 });

      Row() {
        Text(`${this.formatSize(item.receivedBytes)} / ${this.formatSize(item.totalBytes)}`)
          .fontSize(11).fontColor('#9E9E9E').layoutWeight(1);
        if (item.status === DownloadStatus.RUNNING) {
          Text('暂停').fontSize(12).fontColor('#FF9500')
            .onClick(() => { downloadController.pauseDownload(item.id); });
        } else if (item.status === DownloadStatus.PAUSED) {
          Text('继续').fontSize(12).fontColor('#34C759')
            .onClick(() => { downloadController.resumeDownload(item.id); });
        }
        Text('  ').fontSize(12);
        Text('取消').fontSize(12).fontColor('#FF3B30')
          .onClick(() => { downloadController.cancelDownload(item.id); });
        Text('  ').fontSize(12);
        Text('移除').fontSize(12).fontColor('#9E9E9E')
          .onClick(() => { downloadController.removeDownload(item.id); });
      }.width('100%').margin({ top: 4 });

      if (item.error.length > 0) {
        Text(item.error).fontSize(11).fontColor('#FF3B30').margin({ top: 4 });
      }
    }
    .width('100%')
    .padding(12)
    .backgroundColor('#FFFFFF')
    .borderRadius(12);
  }

  private statusLabel(s: DownloadStatus): string {
    switch (s) {
      case DownloadStatus.PENDING: return '等待中';
      case DownloadStatus.RUNNING: return '下载中';
      case DownloadStatus.PAUSED: return '已暂停';
      case DownloadStatus.COMPLETED: return '已完成';
      case DownloadStatus.FAILED: return '失败';
      case DownloadStatus.CANCELLED: return '已取消';
      default: return '';
    }
  }

  private statusColor(s: DownloadStatus): string {
    switch (s) {
      case DownloadStatus.COMPLETED: return '#34C759';
      case DownloadStatus.FAILED:
      case DownloadStatus.CANCELLED: return '#FF3B30';
      case DownloadStatus.RUNNING: return '#007AFF';
      case DownloadStatus.PAUSED: return '#FF9500';
      default: return '#9E9E9E';
    }
  }

  private formatSize(b: number): string {
    if (b < 1024) return `${b} B`;
    if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
    if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
    return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }
}

export default TransfersPage;
