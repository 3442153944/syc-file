/**
 * HomePage 占位（Phase 5 实现：存储用量/在线设备/同步状态/最近下载/快捷入口）。
 * 对标 Android HomeScreen.kt。
 */
@Component
export struct HomePage {
  build() {
    Column() {
      Text('首页').fontSize(24).fontWeight(FontWeight.Bold).margin(20);
      Text('Phase 5 实现中...').fontSize(14).fontColor('#9E9E9E');
    }
    .width('100%')
    .height('100%')
    .justifyContent(FlexAlign.Center)
    .alignItems(HorizontalAlign.Center);
  }
}

export default HomePage;
