import router from '@ohos.router';
import { FileApi } from '../api/file/FileApi';
import type { DiskInfo, AvailableDisksData } from '../api/file/FileTypes';
import { AppRoutes } from '../router/AppRoutes';
import { ResponseException } from '../net/Response';

/**
 * FilesPage
 * 文件页：可用磁盘列表 + 入口（同步设置 / 上传 / 传输列表）。
 * 对标 Android ui/screen/files/* + HomeScreen 的部分功能合并。
 */
@Component
export struct FilesPage {
  @State disks: DiskInfo[] = [];
  @State loading: boolean = false;
  @State errorMsg: string = '';

  aboutToAppear(): void {
    this.loadDisks();
  }

  async loadDisks(): Promise<void> {
    this.loading = true;
    this.errorMsg = '';
    try {
      const resp = await FileApi.getAvailableDisks();
      this.disks = resp.data !== null ? resp.data.allowed_disks : [];
    } catch (e) {
      this.errorMsg = (e as Error)?.message ?? JSON.stringify(e);
    } finally {
      this.loading = false;
    }
  }

  async goBrowse(diskPath: string): Promise<void> {
    const params: Record<string, string> = { 'path': diskPath };
    const opts: router.RouterOptions = { url: AppRoutes.FILE_BROWSE, params: params };
    await router.pushUrl(opts);
  }

  build() {
    Column() {
      // 顶栏
      Row() {
        Text('文件').fontSize(22).fontWeight(FontWeight.Bold).margin({ left: 16 });
        Blank();
        Text('上传').fontSize(14).fontColor('#007AFF').margin({ right: 16 })
          .onClick(() => { router.pushUrl({ url: AppRoutes.UPLOAD }); });
      }.width('100%').height(56).alignItems(VerticalAlign.Center);

      // 入口卡片
      Row() {
        Column() {
          Text('传输列表').fontSize(14);
        }.alignItems(HorizontalAlign.Center).layoutWeight(1)
          .onClick(() => { router.pushUrl({ url: AppRoutes.TRANSFERS }); });
        Column() {
          Text('同步列表').fontSize(14);
        }.alignItems(HorizontalAlign.Center).layoutWeight(1)
          .onClick(() => { router.pushUrl({ url: AppRoutes.SYNC_LIST }); });
        Column() {
          Text('同步文件夹').fontSize(14);
        }.alignItems(HorizontalAlign.Center).layoutWeight(1)
          .onClick(() => { router.pushUrl({ url: AppRoutes.SYNC_FOLDER_MAP }); });
      }
      .width('92%')
      .height(64)
      .backgroundColor('#FFFFFF')
      .borderRadius(12)
      .margin({ top: 12 })
      .justifyContent(FlexAlign.SpaceAround);

      // 可用磁盘
      if (this.loading) {
        Column() {
          LoadingProgress().width(32).height(32);
          Text('加载中...').fontSize(13).fontColor('#9E9E9E').margin({ top: 8 });
        }.width('100%').justifyContent(FlexAlign.Center).margin({ top: 48 });
      } else if (this.errorMsg.length > 0) {
        Column() {
          Text('加载失败').fontSize(14).fontColor('#FF3B30');
          Text(this.errorMsg).fontSize(12).fontColor('#9E9E9E').margin({ top: 4, left: 16, right: 16 });
          Button('重试').type(ButtonType.Capsule).margin({ top: 12 })
            .onClick(() => { this.loadDisks(); });
        }.width('100%').alignItems(HorizontalAlign.Center).margin({ top: 48 });
      } else if (this.disks.length === 0) {
        Column() {
          Text('无可用磁盘').fontSize(14).fontColor('#9E9E9E');
        }.width('100%').alignItems(HorizontalAlign.Center).margin({ top: 48 });
      } else {
        List({ space: 8 }) {
          ForEach(this.disks, (disk: DiskInfo, idx: number) => {
            ListItem() {
              this.diskCard(disk);
            }
          }, (disk: DiskInfo, idx: number) => disk.path + idx.toString());
        }
        .width('92%')
        .margin({ top: 12 })
        .layoutWeight(1);
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }

  @Builder
  diskCard(disk: DiskInfo) {
    Row() {
      Column() {
        Text(disk.path).fontSize(16).fontWeight(FontWeight.Medium);
        Text(`${disk.total_gb} GB / 可用 ${disk.free_gb} GB`).fontSize(12).fontColor('#9E9E9E').margin({ top: 4 });
      }.alignItems(HorizontalAlign.Start).layoutWeight(1);

      // 简易进度条
      Stack({ alignContent: Alignment.Center }) {
        Progress({ value: Math.floor(disk.used_percent), total: 100, type: ProgressType.Ring }).width(48).height(48);
        Text(`${Math.floor(disk.used_percent)}%`).fontSize(11);
      }.width(56).height(56);
    }
    .width('100%')
    .padding(16)
    .backgroundColor('#FFFFFF')
    .borderRadius(12)
    .onClick(() => { this.goBrowse(disk.path); });
  }
}

export default FilesPage;
