import router from '@ohos.router';
import { appConfig } from '../app/AppConfig';
import { cloudLadderStorage } from '../storage/CloudLadderStorage';

/**
 * ServerSettingsPage
 * 服务器地址设置页：编辑 host/port/HTTPS，保存到 AppConfig 持久化。
 * 对标桌面端 ServerSettings.vue。
 */
@Entry
@Component
struct ServerSettingsPage {
  @State server: string = '';
  @State port: string = '';
  @State isHttps: boolean = true;
  @State rootLabel: string = '';
  @State saved: boolean = false;
  @State probeStatus: string = '';
  @State downloadBase: string = '';

  aboutToAppear(): void {
    this.server = appConfig.server;
    this.port = String(appConfig.port);
    this.isHttps = appConfig.isHttps;
    this.rootLabel = cloudLadderStorage.friendlyRoot();
    this.probeStatus = cloudLadderStorage.getInitStatus();
    this.downloadBase = cloudLadderStorage.getDownloadBase();
  }

  async save(): Promise<void> {
    await appConfig.update(
      this.server,
      undefined,
      this.isHttps,
      Number(this.port) || 8891
    );
    this.saved = true;
    setTimeout(() => { this.saved = false; }, 1500);
  }

  build() {
    Column() {
      Row() {
        Text('‹').fontSize(24).padding({ left: 12, right: 8 })
          .onClick(() => { router.back(); });
        Text('服务器设置').fontSize(18).fontWeight(FontWeight.Bold);
      }.width('100%').height(48).alignItems(VerticalAlign.Center);

      Column() {
        Row() {
          Text('域名/IP').fontSize(14).width(80);
          TextInput({ text: this.server })
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.server = v; });
        }.width('100%').margin({ bottom: 12 });

        Row() {
          Text('端口').fontSize(14).width(80);
          TextInput({ text: this.port })
            .type(InputType.Number)
            .layoutWeight(1).height(40)
            .onChange((v: string) => { this.port = v; });
        }.width('100%').margin({ bottom: 12 });

        Row() {
          Text('HTTPS').fontSize(14).width(80);
          Toggle({ type: ToggleType.Switch, isOn: this.isHttps })
            .onChange((v: boolean) => { this.isHttps = v; });
          Blank();
        }.width('100%').margin({ bottom: 12 });

        Column() {
          Row() {
            Text('保存位置').fontSize(14).width(80);
            Text(this.rootLabel)
              .fontSize(13).fontColor('#34C759')
              .layoutWeight(1).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis });
          }.width('100%');
          Text('同步/下载文件保存在此目录，可在系统「文件管理 → Download → 云梯」中查看。')
            .fontSize(11).fontColor('#9E9E9E').width('100%').margin({ top: 6 });
        }.width('100%').margin({ bottom: 12 });

        Column() {
          Text('存储诊断').fontSize(13).fontWeight(FontWeight.Bold).width('100%').margin({ bottom: 6 });
          Text(`getUserDownloadDir: ${this.downloadBase.length > 0 ? this.downloadBase : '(空/不可用)'}`)
            .fontSize(11).fontColor('#666').width('100%').maxLines(2)
            .textOverflow({ overflow: TextOverflow.Ellipsis });
          Text(`状态: ${this.probeStatus}`)
            .fontSize(11).fontColor('#666').width('100%').margin({ top: 4 }).maxLines(3)
            .textOverflow({ overflow: TextOverflow.Ellipsis });
        }.width('100%').margin({ bottom: 24 });

        Button(this.saved ? '已保存' : '保存')
          .type(ButtonType.Capsule)
          .width('100%').height(44)
          .backgroundColor(this.saved ? '#34C759' : '#007AFF')
          .onClick(() => { this.save(); });
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
}

export default ServerSettingsPage;
