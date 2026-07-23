import router from '@ohos.router';
import { SyncApi } from '../api/sync/SyncApi';
import type { SyncFolderInfo } from '../api/sync/SyncTypes';
import { syncMappingStore } from '../sync/SyncMappingStore';
import { cloudLadderStorage } from '../storage/CloudLadderStorage';

/**
 * SyncFolderMapPage
 * 同步文件夹映射页：列出服务器端同步文件夹 + 启用开关。
 * 对标 Android ui/screen/files/SyncFolderMapScreen.kt + SyncMappingStore.kt。
 * 同步文件保存到应用专属公共目录 <Download/云梯>/同步/<name>，用户可在系统「文件管理」中查看。
 */
@Entry
@Component
struct SyncFolderMapPage {
  @State folders: SyncFolderInfo[] = [];
  @State loading: boolean = true;
  @State errorMsg: string = '';
  @State rootLabel: string = '';

  aboutToAppear(): void {
    this.rootLabel = cloudLadderStorage.friendlyRoot();
    this.loadFolders();
  }

  async loadFolders(): Promise<void> {
    this.loading = true;
    this.errorMsg = '';
    try {
      const resp = await SyncApi.listFolders();
      this.folders = resp.data !== null ? resp.data : [];
    } catch (e) {
      this.errorMsg = (e as Error)?.message ?? JSON.stringify(e);
    } finally {
      this.loading = false;
    }
  }

  private directionLabel(dir: string): string {
    if (dir === 'two_way') return '双向同步';
    if (dir === 'upload_only') return '仅上传';
    if (dir === 'download_only') return '仅下载';
    return dir;
  }

  private friendlyPath(folder: SyncFolderInfo): string {
    const root: string = this.rootLabel.length > 0 ? this.rootLabel : '(未设置云梯目录)';
    return `${root}/同步/${folder.name}/`;
  }

  private isLocallyEnabled(folder: SyncFolderInfo): boolean {
    return syncMappingStore.isEnabled(folder.id);
  }

  async toggleEnabled(folder: SyncFolderInfo, enabled: boolean): Promise<void> {
    const localPath: string = cloudLadderStorage.syncFolderDir(folder.name);
    await syncMappingStore.setMapping(folder.id, localPath, enabled);
    this.folders = this.folders.slice();
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('同步文件夹').fontSize(18).fontWeight(FontWeight.Bold);
        Blank();
        Text('刷新').fontSize(14).fontColor('#007AFF').margin({ right: 16 })
          .onClick(() => { this.loadFolders(); });
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      // 云梯目录只读展示（应用专属公共目录，文件管理器可见，无需选择）
      Column() {
        Row() {
          Text('保存位置').fontSize(14).fontWeight(FontWeight.Medium).layoutWeight(1);
          Text(this.rootLabel).fontSize(13).fontColor('#34C759');
        }.width('100%');
        Text('同步文件保存在此目录，可在系统「文件管理 → Download → 云梯」中查看。')
          .fontSize(12).fontColor('#9E9E9E').width('100%').margin({ top: 6 });
      }
      .width('92%').padding(14).backgroundColor('#FFFFFF').borderRadius(12).margin({ top: 4, bottom: 8 });

      Text('同步文件夹由桌面端创建，本设备仅控制是否参与同步。')
        .fontSize(12).fontColor('#9E9E9E').width('92%').margin({ bottom: 8 });

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
            .onClick(() => { this.loadFolders(); });
        }.width('100%').layoutWeight(1).justifyContent(FlexAlign.Center).alignItems(HorizontalAlign.Center);
      } else if (this.folders.length === 0) {
        Column() {
          Text('暂无同步文件夹').fontSize(14).fontColor('#9E9E9E');
          Text('请在 Windows 桌面端创建同步文件夹').fontSize(12).fontColor('#BDBDBD').margin({ top: 4 });
        }.width('100%').layoutWeight(1).justifyContent(FlexAlign.Center).alignItems(HorizontalAlign.Center);
      } else {
        Scroll() {
          Column() {
            ForEach(this.folders, (folder: SyncFolderInfo, idx: number) => {
              Column() {
                this.folderCard(folder);
              }
            }, (folder: SyncFolderInfo, idx: number) => folder.id.toString());
          }
          .width('100%')
          .alignItems(HorizontalAlign.Center)
          .padding({ top: 4, bottom: 24 });
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
  folderCard(folder: SyncFolderInfo) {
    Column() {
      Row() {
        Text(folder.name).fontSize(16).fontWeight(FontWeight.Medium).layoutWeight(1);
        Text(this.directionLabel(folder.direction))
          .fontSize(11).fontColor('#007AFF')
          .padding({ left: 6, right: 6, top: 2, bottom: 2 })
          .backgroundColor('#E8F4FD').borderRadius(4);
      }.width('100%');

      Row() {
        Text('远端路径').fontSize(12).fontColor('#9E9E9E').width(70);
        Text(folder.remote_path).fontSize(12).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
      }.width('100%').margin({ top: 8 });

      Row() {
        Text('本机目录').fontSize(12).fontColor('#9E9E9E').width(70);
        Text(this.friendlyPath(folder)).fontSize(12).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
      }.width('100%').margin({ top: 4 });

      Row() {
        Text('服务器').fontSize(12).fontColor('#9E9E9E').width(70);
        Text(folder.enabled ? '已启用' : '已禁用')
          .fontSize(12)
          .fontColor(folder.enabled ? '#34C759' : '#FF3B30');
      }.width('100%').margin({ top: 4 });

      Row() {
        Text('本机同步').fontSize(14).fontColor('#333333');
        Blank();
        Toggle({ type: ToggleType.Switch, isOn: this.isLocallyEnabled(folder) })
          .selectedColor('#34C759')
          .onChange((isOn: boolean) => { this.toggleEnabled(folder, isOn); })
          .enabled(folder.enabled);
      }.width('100%').margin({ top: 10 });
    }
    .width('92%')
    .padding(14)
    .backgroundColor('#FFFFFF')
    .borderRadius(12)
    .margin({ bottom: 8 });
  }
}

export default SyncFolderMapPage;
