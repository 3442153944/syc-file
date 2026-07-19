import picker from '@ohos.file.picker';
import router from '@ohos.router';
import { ChunkedUploader } from '../api/file/ChunkedUploader';
import type { UploadResult, CancelSignal } from '../api/file/ChunkedUploader';
import { deviceInfoSvc } from '../util/DeviceInfo';
import { appConfig } from '../app/AppConfig';
import { ResponseException } from '../net/Response';

/**
 * UploadPage
 * 上传页：选择本地文件 → 输入远端目录与文件名 → 调 ChunkedUploader 上传（带进度）。
 * 对标 Android ui/screen/files/UploadScreen.kt 简化版。
 */
@Entry
@Component
struct UploadPage {
  @State remotePath: string = '/';
  @State fileName: string = '';
  @State pickedUri: string = '';
  @State uploading: boolean = false;
  @State sent: number = 0;
  @State total: number = 0;
  @State resultMsg: string = '';

  async pickFile(): Promise<void> {
    try {
      const docPicker: picker.DocumentViewPicker = new picker.DocumentViewPicker();
      const uris: string[] = await docPicker.select();
      if (uris.length > 0) {
        this.pickedUri = uris[0];
        // 从 uri 推断文件名（file://.../<name>）
        const idx: number = uris[0].lastIndexOf('/');
        if (idx >= 0) {
          this.fileName = decodeURIComponent(uris[0].substring(idx + 1));
        }
      }
    } catch (e) {
      this.resultMsg = `选择文件失败：${(e as Error).message}`;
    }
  }

  async doUpload(): Promise<void> {
    if (this.pickedUri.length === 0) {
      this.resultMsg = '请先选择文件';
      return;
    }
    if (this.fileName.length === 0) {
      this.resultMsg = '请输入文件名';
      return;
    }
    this.uploading = true;
    this.resultMsg = '';
    this.sent = 0;
    this.total = 0;
    try {
      const cancelSignal: CancelSignal = { cancelled: false };
      const result: UploadResult = await ChunkedUploader.upload({
        filePath: this.pickedUri,
        remotePath: this.remotePath,
        fileName: this.fileName,
        deviceId: deviceInfoSvc.getDeviceId(),
        deleteBeforeUpload: true,
        onProgress: (s: number, t: number): void => {
          this.sent = s;
          this.total = t;
        },
        cancelSignal: cancelSignal,
      });
      if (result.ok) {
        this.resultMsg = result.instant ? '秒传成功' : '上传完成';
      } else {
        this.resultMsg = result.message;
      }
    } catch (e) {
      this.resultMsg = `上传失败：${(e as Error)?.message ?? JSON.stringify(e)}`;
    } finally {
      this.uploading = false;
    }
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('上传文件').fontSize(18).fontWeight(FontWeight.Bold);
      }.width('100%').height(48);

      Column() {
        // 远端目录
        Row() {
          Text('远端目录').fontSize(14).width(80);
          TextInput({ text: this.remotePath })
            .layoutWeight(1)
            .height(40)
            .onChange((v: string) => { this.remotePath = v; });
        }.width('100%').margin({ bottom: 12 });

        // 远端文件名
        Row() {
          Text('文件名').fontSize(14).width(80);
          TextInput({ text: this.fileName })
            .layoutWeight(1)
            .height(40)
            .onChange((v: string) => { this.fileName = v; });
        }.width('100%').margin({ bottom: 12 });

        // 选择文件按钮
        Button('选择本地文件')
          .type(ButtonType.Capsule)
          .width('100%')
          .height(44)
          .backgroundColor('#F2F2F2')
          .fontColor('#333333')
          .margin({ bottom: 8 })
          .onClick(() => { this.pickFile(); });

        if (this.pickedUri.length > 0) {
          Text(`已选：${this.pickedUri}`).fontSize(11).fontColor('#9E9E9E')
            .maxLines(2).margin({ bottom: 12 });
        }

        // 上传按钮
        Button(this.uploading ? '上传中...' : '开始上传')
          .type(ButtonType.Capsule)
          .width('100%')
          .height(48)
          .backgroundColor('#007AFF')
          .enabled(!this.uploading)
          .onClick(() => { this.doUpload(); });

        if (this.total > 0) {
          Progress({ value: this.total > 0 ? Math.floor((this.sent / this.total) * 100) : 0, total: 100, type: ProgressType.Linear })
            .width('100%')
            .margin({ top: 12 });
          Text(`${this.formatSize(this.sent)} / ${this.formatSize(this.total)}`)
            .fontSize(11).fontColor('#9E9E9E').margin({ top: 4 });
        }

        if (this.resultMsg.length > 0) {
          Text(this.resultMsg)
            .fontSize(13)
            .fontColor(this.resultMsg.includes('失败') || this.resultMsg.includes('错误') ? '#FF3B30' : '#34C759')
            .margin({ top: 12 });
        }
      }
      .width('92%')
      .padding(16)
      .backgroundColor('#FFFFFF')
      .borderRadius(12)
      .margin({ top: 12 });
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#F5F5F5');
  }

  private formatSize(b: number): string {
    if (b < 1024) return `${b} B`;
    if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
    if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
    return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }
}

export default UploadPage;
