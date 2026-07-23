import router from '@ohos.router';
import { promptAction } from '@kit.ArkUI';
import { FileApi } from '../api/file/FileApi';
import type { FileItem, TraverseDirectoryData } from '../api/file/FileTypes';

/**
 * FileBrowsePage
 * 远端目录浏览页：点目录深入、点文件直接下载到应用专属公共目录 Download/云梯/下载。
 * 文件管理器可见，无需每次选位置。
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

  /** 下载暂时停用：先用主页「目录写入测试」确认哪种路径能写公共 Download。 */
  async startDownload(item: FileItem): Promise<void> {
    promptAction.showToast({ message: '下载调试中，请先用主页「目录写入测试」按钮' });
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
      } else {
        Scroll() {
          List({ space: 0 }) {
            ForEach(this.items, (item: FileItem, idx: number) => {
              ListItem() {
                this.itemRow(item);
              }
            }, (item: FileItem, idx: number) => item.path + item.name + idx.toString());
          }
          .width('100%')
          .backgroundColor('#FFFFFF');
        }
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
      Text(item.is_dir ? '📁' : '📄').fontSize(20).margin({ right: 12 });
      Column() {
        Text(item.name).fontSize(15).maxLines(1);
        Text(this.formatSize(item.size)).fontSize(11).fontColor('#9E9E9E').margin({ top: 2 });
      }.alignItems(HorizontalAlign.Start).layoutWeight(1);
      if (!item.is_dir) {
        Text('下载').fontSize(13).fontColor('#007AFF');
      }
    }
    .width('100%')
    .padding({ left: 16, right: 16, top: 12, bottom: 12 })
    .border({ width: { bottom: 1 }, color: '#F0F0F0' })
    .onClick(() => { this.onItemTap(item); });
  }

  private formatSize(b: number): string {
    if (b < 1024) return `${b} B`;
    if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
    if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
    return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }
}

export default FileBrowsePage;
