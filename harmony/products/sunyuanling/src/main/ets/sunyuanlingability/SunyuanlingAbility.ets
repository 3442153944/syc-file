import { ConfigurationConstant, UIAbility } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { window } from '@kit.ArkUI';
import { TokenManager } from '../token/tokenManager';
import { userStore } from '../app/UserStore';
import { appConfig } from '../app/AppConfig';
import { downloadController } from '../download/DownloadController';
import { deviceInfoSvc } from '../util/DeviceInfo';

const DOMAIN = 0x0000;

export default class SunyuanlingAbility extends UIAbility {
  async onCreate(): Promise<void> {
    try {
      this.context.getApplicationContext().setColorMode(ConfigurationConstant.ColorMode.COLOR_MODE_NOT_SET);
      // 1. token 单例
      await TokenManager.getInstance().init(this.context);
      // 2. 配置单例（持久化服务器地址等）
      await appConfig.init(this.context);
      // 3. 当前用户单例（缓存到 AppStorage，供 @StorageLink 订阅）
      await userStore.init(this.context);
      // 4. 设备 id 单例（同步派发排除源设备用）
      await deviceInfoSvc.init(this.context);
      // 5. 下载控制器（绑定 Context + AppStorage key）
      downloadController.setContext(this.context);
    } catch (err) {
      hilog.error(DOMAIN, 'testTag', 'Init failed: %{public}s', JSON.stringify(err));
    }
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onCreate');
  }

  onDestroy(): void {
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onDestroy');
  }

  onWindowStageCreate(windowStage: window.WindowStage): void {
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onWindowStageCreate');
    windowStage.loadContent('pages/Index', (err) => {
      if (err.code) {
        hilog.error(DOMAIN, 'testTag', 'Failed to load the content. Cause: %{public}s', JSON.stringify(err));
        return;
      }
      hilog.info(DOMAIN, 'testTag', 'Succeeded in loading the content.');
    });
  }

  onWindowStageDestroy(): void {
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onWindowStageDestroy');
  }

  onForeground(): void {
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onForeground');
  }

  onBackground(): void {
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onBackground');
  }
}
