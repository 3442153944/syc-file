import router from '@ohos.router';
import fs from '@ohos.file.fs';
import { Environment } from '@kit.CoreFileKit';
import { common } from '@kit.AbilityKit';
import { BusinessError, request } from '@kit.BasicServicesKit';
import { DownloadFileButton, DownloadIconStyle, DownloadDescription } from '@kit.ArkUI';
import { picker, fileUri } from '@kit.CoreFileKit';
import { FileApi } from '../api/file/FileApi';
import { WsApi } from '../api/ws/WsApi';
import type { DiskInfo, AvailableDisksData, DownloadHistoryItem, DownloadHistoryData } from '../api/file/FileTypes';
import { AppRoutes } from '../router/AppRoutes';
import { wsManager, WsState } from '../net/WebSocketManager';
import { userStore, USER_STORAGE_KEY } from '../app/UserStore';
import type { UserInfo } from '../api/user/UserTypes';

/**
 * HomePage
 * 仪表盘：存储用量 / 在线设备数 / 同步状态 / 最近下载 / 快捷入口。
 * 对标 Android HomeScreen.kt。
 */
@Component
export struct HomePage {
  @StorageLink(USER_STORAGE_KEY) currentUser: UserInfo | null = null;
  @State totalBytes: number = 0;
  @State usedBytes: number = 0;
  @State deviceCount: number = 0;
  @State wsConnected: boolean = false;
  @State recentDownloads: DownloadHistoryItem[] = [];
  @State loading: boolean = true;
  @State testResult: string = '点下面按钮测试往 Download/<包名>/1.txt 写 "123"';
  private unregWs: (() => void) | null = null;

  // ====== 目录写入测试（验证哪种路径能写到公共 Download，文件管理器可见）======

  private bundleName(): string {
    const ctx = getContext(this) as common.UIAbilityContext;
    return ctx.applicationInfo.name;
  }

  /** 异步写 <dir>/1.txt = "123"（异步 fs，不阻塞主线程，避免 appfreeze）。 */
  private async probeWrite(tag: string, dir: string): Promise<void> {
    const filePath: string = `${dir}/1.txt`;
    try {
      await fs.mkdir(dir, true);
      const f: fs.File = await fs.open(filePath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      await fs.write(f.fd, '123');
      await fs.close(f.fd);
      this.testResult = `✅[${tag}] 成功\n${filePath}`;
    } catch (e) {
      const err = e as BusinessError;
      this.testResult = `❌[${tag}] 失败 code=${err.code} msg=${err.message}\n${filePath}`;
    }
  }

  /** 方式1：Environment.getUserDownloadDir()/<包名>/1.txt */
  // private async testDownloadDir(): Promise<void> {
  //   let d: string = '';
  //   try {
  //     d = Environment.getUserDownloadDir();
  //   } catch (e) {
  //     const err = e as BusinessError;
  //     this.testResult = `❌[getUserDownloadDir] 取路径异常 code=${err.code} msg=${err.message}`;
  //     return;
  //   }
  //   await this.probeWrite('getUserDownloadDir', `${d}/${this.bundleName()}`);
  // }

  /** 方式2：<filesDir>/../Download/<包名>/1.txt */
  private async testFilesDirParent(): Promise<void> {
    const ctx = getContext(this) as common.UIAbilityContext;
    await this.probeWrite('filesDir/..', `${ctx.filesDir}/../Download/${this.bundleName()}`);
  }

  /** 方式3：/storage/Users/currentUser/Download/<包名>/1.txt */
  private async testAbsolute(): Promise<void> {
    await this.probeWrite('绝对路径', `/storage/Users/currentUser/Download/${this.bundleName()}`);
  }

  /**
   * 方式4：系统下载服务 request.agent（QQ/浏览器同款）。
   * 下载一个公网小文件（百度 favicon），完成后扫描候选路径，把实际落盘位置显示到文本框。
   */
  private async testRequestAgent(): Promise<void> {
    const ctx = getContext(this) as common.UIAbilityContext;
    const bundle: string = ctx.applicationInfo.name;
    const fileName: string = 'ra_probe.ico';
    this.testResult = 'request.agent 启动中...';
    try {
      const config: request.agent.Config = {
        action: request.agent.Action.DOWNLOAD,
        url: 'https://www.baidu.com/favicon.ico',
        method: 'GET',
        saveas: `./${fileName}`,
        mode: request.agent.Mode.BACKGROUND,
        overwrite: true,
        gauge: false,
        network: request.agent.Network.ANY,
        title: '云梯下载探针',
      };
      const task: request.agent.Task = await request.agent.create(ctx, config);
      task.on('completed', (progress: request.agent.Progress) => {
        const candidates: string[] = [
          `${ctx.filesDir}/../Download/${bundle}/${fileName}`,
          `/storage/Users/currentUser/Download/${bundle}/${fileName}`,
          `${ctx.cacheDir}/${fileName}`,
          `${ctx.filesDir}/${fileName}`,
        ];
        let found: string = '(4 个候选都没找到)';
        for (let i = 0; i < candidates.length; i++) {
          let ok: boolean = false;
          try { ok = fs.accessSync(candidates[i]); } catch (e) { ok = false; }
          if (ok) { found = candidates[i]; break; }
        }
        this.testResult = `✅request.agent 完成\n落盘: ${found}`;
      });
      task.on('failed', (progress: request.agent.Progress) => {
        this.testResult = `❌request.agent 失败 state=${progress.state}`;
      });
      await task.start();
      this.testResult = 'request.agent 任务已启动，等待完成...';
    } catch (e) {
      const err = e as BusinessError;
      this.testResult = `❌request.agent 异常 code=${err.code} msg=${err.message}`;
    }
  }

  /**
   * 方式5：DownloadFileButton 安全组件（QQ 无弹框机制）。
   * 点击该系统按钮即授权，此时尝试拿 Download 路径并写 1.txt，看点击授权后哪种路径能成。
   */
  // private async testDownloadButton(): Promise<void> {
  //   const ctx = getContext(this) as common.UIAbilityContext;
  //   const bundle: string = ctx.applicationInfo.name;
  //   let head: string = 'DLBtn点击后:\n';
  //   let d: string = '';
  //   try {
  //     d = Environment.getUserDownloadDir();
  //     head += `getUserDownloadDir=${d}\n`;
  //   } catch (e) {
  //     const err = e as BusinessError;
  //     head += `getUserDownloadDir失败 code=${err.code}\n`;
  //   }
  //   // 授权后优先用 getUserDownloadDir 返回值，否则退到绝对路径
  //   const base: string = d.length > 0 ? d : `/storage/Users/currentUser/Download`;
  //   const dir: string = `${base}/${bundle}`;
  //   const filePath: string = `${dir}/1.txt`;
  //   try {
  //     await fs.mkdir(dir, true);
  //     const f: fs.File = await fs.open(filePath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
  //     await fs.write(f.fd, '123');
  //     await fs.close(f.fd);
  //     this.testResult = `${head}✅成功: ${filePath}`;
  //   } catch (e) {
  //     const err = e as BusinessError;
  //     this.testResult = `${head}❌失败 code=${err.code} msg=${err.message}\n${filePath}`;
  //   }
  // }

  /**
   * 方式6：DocumentViewPicker.save() + pickerMode=DOWNLOAD（QQ/DownloadFileButton 内部机制）。
   * 直接存到 Download/应用名、不弹选位置框、不依赖 getUserDownloadDir（绕开 801）。
   */
  private async testPickerDownloadMode(): Promise<void> {
    const ctx = getContext(this) as common.UIAbilityContext;
    this.testResult = 'picker DOWNLOAD 拿授权目录...';
    let dirUri: string = '';
    // 1) picker DOWNLOAD 模式拿 Download/应用名 目录 uri（无弹框）
    try {
      const options: picker.DocumentSaveOptions = new picker.DocumentSaveOptions();
      options.newFileNames = ['ra_dl.ico'];
      options.pickerMode = picker.DocumentPickerMode.DOWNLOAD;
      const viewPicker: picker.DocumentViewPicker = new picker.DocumentViewPicker(ctx);
      const result: string[] = await viewPicker.save(options);
      if (result.length === 0) {
        this.testResult = '❌picker DOWNLOAD 返回空数组';
        return;
      }
      dirUri = result[0];
    } catch (e) {
      const err = e as BusinessError;
      this.testResult = `❌picker DOWNLOAD 失败 code=${err.code} msg=${err.message}`;
      return;
    }
    // 2) FileUri 把授权目录 uri 转成真实 path，再用 fs 直接写文件
    try {
      const dirPath: string = new fileUri.FileUri(dirUri).path;
      const filePath: string = `${dirPath}/1.txt`;
      const f: fs.File = fs.openSync(filePath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      fs.writeSync(f.fd, '123');
      fs.closeSync(f);
      this.testResult = `✅✅写入公共目录成功!\n目录uri=${dirUri}\npath=${dirPath}\n文件=${filePath}`;
    } catch (e) {
      const err = e as BusinessError;
      this.testResult = `目录uri=${dirUri}\nFileUri转换/写失败 code=${err.code} ${err.message}`;
    }
  }

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

        // ====== 目录写入测试台（临时，验证哪种路径能写公共 Download）======
        Column() {
          Text('目录写入测试').fontSize(14).fontWeight(FontWeight.Medium).width('100%');
          Text(this.testResult)
            .fontSize(12).fontColor('#333333').width('100%').margin({ top: 6 });
          Row() {
            // Button('方式1 DownloadDir').fontSize(11).height(34).layoutWeight(1)
            //   .onClick(() => { this.testDownloadDir(); });
            Button('方式2 filesDir/..').fontSize(11).height(34).layoutWeight(1).margin({ left: 6 })
              .onClick(() => { this.testFilesDirParent(); });
          }.width('100%').margin({ top: 8 });
          Row() {
            Button('方式3 绝对路径').fontSize(11).height(34).layoutWeight(1)
              .onClick(() => { this.testAbsolute(); });
            Button('方式4 request.agent').fontSize(11).height(34).layoutWeight(1).margin({ left: 6 })
              .backgroundColor('#FF9500')
              .onClick(() => { this.testRequestAgent(); });
          }.width('100%').margin({ top: 6 });
          // 方式5：DownloadFileButton 安全组件（点击即授权，QQ 无弹框机制）
          // Row() {
          //   DownloadFileButton({
          //     contentOptions: {
          //       icon: DownloadIconStyle.FULL_FILLED,
          //       text: DownloadDescription.DOWNLOAD
          //     }
          //   })
          //     .backgroundColor('#34C759')
          //     .borderRadius(8)
          //     .padding({ left: 12, right: 12, top: 6, bottom: 6 })
          //     .onClick(() => { this.testDownloadButton(); });
          //   Text('← 方式5 点这个安全组件').fontSize(11).fontColor('#9E9E9E').margin({ left: 8 });
          // }.width('100%').margin({ top: 8 }).alignItems(VerticalAlign.Center);
          Row() {
            Button('方式6 picker DOWNLOAD 模式（重点）').fontSize(11).height(34).layoutWeight(1)
              .backgroundColor('#AF52DE')
              .onClick(() => { this.testPickerDownloadMode(); });
          }.width('100%').margin({ top: 6 });
        }
        .width('92%').padding(14).backgroundColor('#FFF8E1').borderRadius(12).margin({ top: 8 });

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
