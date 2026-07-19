/**
 * MonitorPage 占位（Phase 5 实现：服务器状态 + 在线设备列表）。
 */
@Component
export struct MonitorPage {
  build() {
    Column() {
      Text('监控').fontSize(24).fontWeight(FontWeight.Bold).margin(20);
      Text('Phase 5 实现中...').fontSize(14).fontColor('#9E9E9E');
    }
    .width('100%')
    .height('100%')
    .justifyContent(FlexAlign.Center)
    .alignItems(HorizontalAlign.Center);
  }
}

export default MonitorPage;
