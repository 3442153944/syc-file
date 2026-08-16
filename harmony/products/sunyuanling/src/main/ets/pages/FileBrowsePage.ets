import router from '@ohos.router';
import { promptAction } from '@kit.ArkUI';
import { FileApi } from '../api/file/FileApi';
import type { FileItem, TraverseDirectoryData, DownloadParams } from '../api/file/FileTypes';
import { downloadController } from '../download/DownloadController';
import { deviceInfoSvc } from '../util/DeviceInfo';

/**
 * FileBrowsePage
 * 远端目录浏览页：点目录深入、点文件下载到公共目录 <Download/包名>/下载/。
 * 文件管理器可见，无需每次选位置；进度在「传输列表」查看。
 */
@Entry
@Component
struct FileBrowsePage {
  @State currentPath: string = '';
  @State items: FileItem[] = [];
  @State loading: boolean = false;
  @State errorMsg: string = '';
  @State pathStack: string[] = [];

  aboutToAppear(): void {
    const params: Record<string, string> = router.getParams() as Record<string, string>;
    if (params && params.path !== undefined) {
      this.currentPath = params.path;
      this.pathStack = [this.currentPath];
      this.loadDir(this.currentPath);
    }
  }

  async loadDir(path: string): Promise<void> {
    this.loading = true;
    this.errorMsg = '';
    try {
      const resp = await FileApi.traverseDirectory({
        path: path,
        page: 0,
        page_size: 0,
      });
      if (resp.data !== null) {
        this.currentPath = resp.data.current_path;
        this.items = resp.data.items;
      }
    } catch (e) {
      this.errorMsg = (e as Error)?.message ?? JSON.stringify(e);
    } finally {
      this.loading = false;
    }
  }

  async onItemTap(item: FileItem): Promise<void> {
    if (item.is_dir) {
      this.pathStack.push(item.path);
      await this.loadDir(item.path);
    } else {
      await this.startDownload(item);
    }
  }

  /**
   * 下载到公共目录 `<Download/包名>/下载/`（DownloadController 自己会挑落盘位置，
   * 未授权时落沙盒并在拿到授权后归位）。进度在「传输列表」里看。
   *
   * ⚠ 服务端 `/file/download` 做的是 `filepath.Join(path, name)`，
   *   所以 path 必须是**文件所在目录**（= 当前浏览路径），name 是叶子文件名。
   */
  async startDownload(item: FileItem): Promise<void> {
    try {
      const params: DownloadParams = {
        path: this.currentPath,
        name: item.name,
        device_id: deviceInfoSvc.getDeviceId(),
      };
      await downloadController.addDownload(params);
      promptAction.showToast({ message: `已加入下载：${item.name}` });
    } catch (e) {
      const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
      promptAction.showToast({ message: `下载失败：${msg}` });
    }
  }

  goBack(): void {
    if (this.pathStack.length > 1) {
      this.pathStack.pop();
      const prev: string = this.pathStack[this.pathStack.length - 1];
      this.loadDir(prev);
    } else {
      router.back();
    }
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { this.goBack(); });
        Text(this.currentPath).fontSize(14).maxLines(2).layoutWeight(1);
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      if (this.loading) {
        Column() {
          LoadingProgress().width(32).height(32);
          Text('加载中...').fontSize(13).fontColor('#9E9E9E').margin({ top: 8 });
        }.width('100%').justifyContent(FlexAlign.Center).margin({ top: 48 });
      } else if (this.errorMsg.length > 0) {
        Column() {
          Text('加载失败').fontSize(14).fontColor('#FF3B30');
          Text(this.errorMsg).fontSize(12).fontColor('#9E9E9E').margin({ top: 4 });
          Button('重试').type(ButtonType.Capsule).margin({ top: 12 })
            .onClick(() => { this.loadDir(this.currentPath); });
        }.width('100%').alignItems(HorizontalAlign.Center).margin({ top: 48 });
      } else if (this.items.length === 0) {
        Column() {
          Text('该目录为空').fontSize(14).fontColor('#9E9E9E');
        }.width('100%').alignItems(HorizontalAlign.Center).margin({ top: 48 });
      } else {
        // ⚠ 不要再把 List 套进 Scroll：Scroll 会给子组件无限高度，List 的懒加载和滚动
        //   全部失效（表现为列表高度算错、滚动怪异）。List 自己就是可滚容器，直接 layoutWeight。
        List({ space: 0 }) {
          ForEach(this.items, (item: FileItem, idx: number) => {
            ListItem() {
              this.itemRow(item);
            }
          }, (item: FileItem, idx: number) => item.path + item.name + idx.toString());
        }
        .width('100%')
        .backgroundColor('#FFFFFF')
        .layoutWeight(1);
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }

  @Builder
  itemRow(item: FileItem) {
    Row() {
      Text(item.is_dir ? '📁' : '📄').fontSize(20).width(28).textAlign(TextAlign.Center);
      Column() {
        Text(item.name).fontSize(15)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          .width('100%').textAlign(TextAlign.Start);
        Text(this.subtitleOf(item)).fontSize(11).fontColor('#9E9E9E')
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          .width('100%').textAlign(TextAlign.Start)
          .margin({ top: 2 });
      }.alignItems(HorizontalAlign.Start).layoutWeight(1).margin({ left: 12 });
      if (item.is_dir) {
        Text('›').fontSize(20).fontColor('#9E9E9E');
      } else {
        Text('下载').fontSize(13).fontColor('#007AFF');
      }
    }
    .width('100%')
    .alignItems(VerticalAlign.Center)
    .padding({ left: 16, right: 16, top: 12, bottom: 12 })
    .border({ width: { bottom: 1 }, color: '#F0F0F0' })
    .onClick(() => { this.onItemTap(item); });
  }

  /**
   * 副标题：目录报子项数，文件报大小 + 修改时间（对标 Android 文件列表）。
   * 目录的 `size` 是文件系统给目录项本身的大小（Windows 上常是 0），拿来显示纯属误导。
   */
  private subtitleOf(item: FileItem): string {
    const t: string = this.formatTime(item.mod_time);
    if (item.is_dir) {
      const n: string = `${item.children_count} 项`;
      return t.length > 0 ? `${n} · ${t}` : n;
    }
    const s: string = this.formatSize(item.size);
    return t.length > 0 ? `${s} · ${t}` : s;
  }

  private formatTime(t: string): string {
    if (t === null || t === undefined || t.length === 0) return '';
    const d = new Date(t);
    const ms: number = d.getTime();
    if (isNaN(ms) || d.getFullYear() < 1980) return '';
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      + ` ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  }

  private formatSize(b: number): string {
    if (b === null || b === undefined || isNaN(b)) return '未知大小';
    if (b < 1024) return `${b} B`;
    if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
    if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
    return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }
}

export default FileBrowsePage;
