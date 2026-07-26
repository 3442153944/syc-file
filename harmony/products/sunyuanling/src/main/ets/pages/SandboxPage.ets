import router from '@ohos.router';
import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';
import { promptAction } from '@kit.ArkUI';
import { readTextFile } from '../storage/JsonFileStore';

/**
 * SandboxPage
 * 应用沙盒浏览器：看得见、管得着自己写进沙盒的东西。
 *
 * 为什么需要：同步的三本台账（基线 / 暂存清单 / 搬运流水）和后台暂存的文件都躺在沙盒里，
 * 而沙盒对用户完全不可见——出问题时既看不到台账内容，也不知道暂存区堆了多少东西、
 * 更没法清理。这个页面把这块打开：浏览、看文本内容、按需删除、看占用。
 *
 * 沙盒是应用私有目录，`fs.listFile` 在这里是可靠的
 * （跟 picker 临时授权的公共目录不同，见 sync/SyncBaseStore 注释）。
 */

/** 一行条目。 */
interface SandboxItem {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  /** 秒 */
  mtime: number;
}

/** 可切换的沙盒根。 */
interface SandboxRoot {
  label: string;
  path: string;
}

/** 文本类文件才提供「查看内容」。 */
const TEXT_EXT: string[] = ['.json', '.txt', '.log', '.md', '.xml', '.ini', '.csv'];
/** 预览最多显示的字符数，避免大文件把 UI 撑爆。 */
const PREVIEW_LIMIT = 20000;

@Entry
@Component
struct SandboxPage {
  @State roots: SandboxRoot[] = [];
  @State rootIndex: number = 0;
  @State cwd: string = '';
  @State items: SandboxItem[] = [];
  @State loading: boolean = false;
  @State errorMsg: string = '';
  @State totalSize: number = 0;
  /** 文本预览弹层 */
  @State previewTitle: string = '';
  @State previewText: string = '';
  @State previewVisible: boolean = false;

  aboutToAppear(): void {
    const ctx = this.getUIContext().getHostContext() as common.UIAbilityContext;
    const filesRoot: SandboxRoot = { label: '文件目录', path: ctx.filesDir };
    const cacheRoot: SandboxRoot = { label: '缓存目录', path: ctx.cacheDir };
    const tempRoot: SandboxRoot = { label: '临时目录', path: ctx.tempDir };
    this.roots = [filesRoot, cacheRoot, tempRoot];
    this.cwd = filesRoot.path;
    this.load();
  }

  /** 当前根路径。 */
  private rootPath(): string {
    return this.roots.length > 0 ? this.roots[this.rootIndex].path : '';
  }

  /** 相对当前根的展示路径。 */
  private displayPath(): string {
    const root: string = this.rootPath();
    if (this.cwd === root) return '/';
    return this.cwd.startsWith(root) ? this.cwd.substring(root.length) : this.cwd;
  }

  private canGoUp(): boolean {
    return this.cwd !== this.rootPath();
  }

  async load(): Promise<void> {
    this.loading = true;
    this.errorMsg = '';
    const list: SandboxItem[] = [];
    let total: number = 0;
    try {
      const names: string[] = await fs.listFile(this.cwd);
      for (let i = 0; i < names.length; i++) {
        const name: string = names[i];
        const full: string = `${this.cwd}/${name}`;
        try {
          const st: fs.Stat = await fs.stat(full);
          const isDir: boolean = st.isDirectory();
          const item: SandboxItem = {
            name, path: full, isDir,
            size: isDir ? 0 : st.size,
            mtime: Math.floor(st.mtime),
          };
          list.push(item);
          total += item.size;
        } catch (e) {
          // 单个条目 stat 失败不影响整体浏览
        }
      }
      // 目录在前，其次按名字
      list.sort((a: SandboxItem, b: SandboxItem): number => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0);
      });
    } catch (e) {
      this.errorMsg = `读取失败: ${e instanceof Error ? e.message : JSON.stringify(e)}`;
    }
    this.items = list;
    this.totalSize = total;
    this.loading = false;
  }

  private enter(item: SandboxItem): void {
    if (item.isDir) {
      this.cwd = item.path;
      this.load();
    } else if (this.isTextFile(item.name)) {
      this.preview(item);
    } else {
      promptAction.showToast({ message: '仅支持预览文本类文件' });
    }
  }

  private goUp(): void {
    if (!this.canGoUp()) return;
    const idx: number = this.cwd.lastIndexOf('/');
    if (idx <= 0) return;
    const parent: string = this.cwd.substring(0, idx);
    this.cwd = parent.length < this.rootPath().length ? this.rootPath() : parent;
    this.load();
  }

  private isTextFile(name: string): boolean {
    const lower: string = name.toLowerCase();
    for (let i = 0; i < TEXT_EXT.length; i++) {
      if (lower.endsWith(TEXT_EXT[i])) return true;
    }
    return false;
  }

  private async preview(item: SandboxItem): Promise<void> {
    const raw: string = await readTextFile(item.path);
    this.previewTitle = item.name;
    this.previewText = raw.length > PREVIEW_LIMIT
      ? `${raw.substring(0, PREVIEW_LIMIT)}\n\n…（已截断，共 ${raw.length} 字符）`
      : (raw.length === 0 ? '（空文件或读取失败）' : raw);
    this.previewVisible = true;
  }

  /** 删除前确认——沙盒里放的是台账和暂存文件，误删会让同步重来一轮。 */
  private confirmDelete(item: SandboxItem): void {
    this.getUIContext().showAlertDialog({
      title: '确认删除',
      message: `${item.isDir ? '目录' : '文件'}：${item.name}\n`
        + `${item.isDir ? '将连同其中所有内容一起删除。' : ''}\n`
        + '注意：删除同步台账会导致下一轮全量重新比对。',
      primaryButton: { value: '取消', action: () => {} },
      secondaryButton: {
        value: '删除',
        fontColor: '#FF3B30',
        action: () => { this.doDelete(item); },
      },
    });
  }

  private async doDelete(item: SandboxItem): Promise<void> {
    try {
      if (item.isDir) {
        await fs.rmdir(item.path);   // 递归
      } else {
        await fs.unlink(item.path);
      }
      promptAction.showToast({ message: '已删除' });
      await this.load();
    } catch (e) {
      promptAction.showToast({ message: `删除失败: ${e instanceof Error ? e.message : JSON.stringify(e)}` });
    }
  }

  private formatBytes(bytes: number): string {
    if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
  }

  private formatTime(sec: number): string {
    if (sec <= 0) return '';
    const d: Date = new Date(sec * 1000);
    const mm: string = (d.getMonth() + 1).toString().padStart(2, '0');
    const dd: string = d.getDate().toString().padStart(2, '0');
    const hh: string = d.getHours().toString().padStart(2, '0');
    const mi: string = d.getMinutes().toString().padStart(2, '0');
    return `${mm}-${dd} ${hh}:${mi}`;
  }

  build() {
    Stack() {
      Column() {
        // 顶栏
        Row() {
          Text('‹').fontSize(24).padding({ left: 12, right: 8 })
            .onClick(() => { router.back(); });
          Text('沙盒空间').fontSize(18).fontWeight(FontWeight.Bold);
          Blank();
          Text('刷新').fontSize(14).fontColor('#007AFF').padding({ right: 16 })
            .onClick(() => { this.load(); });
        }.width('100%').height(48).alignItems(VerticalAlign.Center);

        // 根目录切换
        Row() {
          ForEach(this.roots, (r: SandboxRoot, idx: number) => {
            Text(r.label)
              .fontSize(13)
              .fontColor(this.rootIndex === idx ? '#FFFFFF' : '#666666')
              .backgroundColor(this.rootIndex === idx ? '#007AFF' : '#FFFFFF')
              .borderRadius(14)
              .padding({ left: 14, right: 14, top: 6, bottom: 6 })
              .margin({ right: 8 })
              .onClick(() => {
                this.rootIndex = idx;
                this.cwd = r.path;
                this.load();
              });
          }, (r: SandboxRoot) => r.path);
        }.width('92%').margin({ top: 4, bottom: 8 });

        // 路径 + 统计
        Row() {
          if (this.canGoUp()) {
            Text('⬆ 上级').fontSize(12).fontColor('#007AFF').margin({ right: 10 })
              .onClick(() => { this.goUp(); });
          }
          Text(this.displayPath())
            .fontSize(12).fontColor('#666666').layoutWeight(1)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
          Text(`${this.items.length} 项 · ${this.formatBytes(this.totalSize)}`)
            .fontSize(11).fontColor('#9E9E9E');
        }
        .width('92%').padding({ top: 8, bottom: 8 })
        .border({ width: { bottom: 1 }, color: '#F0F0F0' });

        if (this.errorMsg.length > 0) {
          Text(this.errorMsg).fontSize(13).fontColor('#FF3B30').width('92%').margin({ top: 12 });
        }

        if (this.loading) {
          Text('加载中…').fontSize(13).fontColor('#9E9E9E').margin({ top: 16 });
        } else if (this.items.length === 0 && this.errorMsg.length === 0) {
          Text('这个目录是空的').fontSize(13).fontColor('#9E9E9E').margin({ top: 24 });
        } else {
          List({ space: 0 }) {
            ForEach(this.items, (item: SandboxItem) => {
              ListItem() {
                Row() {
                  Text(item.isDir ? '📁' : (this.isTextFile(item.name) ? '📄' : '📦'))
                    .fontSize(16).width(28);
                  Column() {
                    Text(item.name)
                      .fontSize(14).width('100%')
                      .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
                    Text(item.isDir
                      ? this.formatTime(item.mtime)
                      : `${this.formatBytes(item.size)} · ${this.formatTime(item.mtime)}`)
                      .fontSize(11).fontColor('#9E9E9E').width('100%').margin({ top: 2 });
                  }.layoutWeight(1).alignItems(HorizontalAlign.Start);
                  Text('删除')
                    .fontSize(12).fontColor('#FF3B30').padding({ left: 10, right: 4 })
                    .onClick(() => { this.confirmDelete(item); });
                }
                .width('100%').padding({ left: 4, right: 4, top: 12, bottom: 12 })
                .alignItems(VerticalAlign.Center)
                .onClick(() => { this.enter(item); });
              }
            }, (item: SandboxItem) => item.path);
          }
          .width('92%').layoutWeight(1)
          .divider({ strokeWidth: 1, color: '#F5F5F5' });
        }
      }
      .width('100%').height('100%').backgroundColor('#F5F5F5')
      .alignItems(HorizontalAlign.Center);

      // 文本预览浮层
      if (this.previewVisible) {
        Column() {
          Row() {
            Text(this.previewTitle)
              .fontSize(15).fontWeight(FontWeight.Medium).layoutWeight(1)
              .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
            Text('关闭').fontSize(14).fontColor('#007AFF')
              .onClick(() => { this.previewVisible = false; });
          }.width('100%').padding({ bottom: 10 });

          Scroll() {
            Text(this.previewText)
              .fontSize(11).fontColor('#333333').width('100%')
              .textAlign(TextAlign.Start);
          }
          .layoutWeight(1).width('100%')
          .scrollBar(BarState.Auto)
          .align(Alignment.TopStart);
        }
        .width('92%').height('76%')
        .padding(16).backgroundColor('#FFFFFF').borderRadius(12);
      }
    }
    .width('100%').height('100%');
  }
}

export default SandboxPage;
