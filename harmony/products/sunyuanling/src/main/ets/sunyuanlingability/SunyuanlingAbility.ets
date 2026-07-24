import { ConfigurationConstant, UIAbility } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { window } from '@kit.ArkUI';
import { TokenManager } from '../token/tokenManager';
import { userStore } from '../app/UserStore';
import { appConfig } from '../app/AppConfig';
import { downloadController } from '../download/DownloadController';
import { downloadStore } from '../download/DownloadStore';
import { deviceInfoSvc } from '../util/DeviceInfo';
import { syncMappingStore } from '../sync/SyncMappingStore';
import { backgroundTaskHelper } from '../service/BackgroundTaskHelper';

const DOMAIN = 0x0000;

export default class SunyuanlingAbility extends UIAbility {
  async onCreate(): Promise<void> {
    // 启动探针：真机 hilog 过滤 "云梯Boot" 即可确认本版本代码是否真正冷启动生效
    hilog.info(DOMAIN, '云梯Boot', 'onCreate v4: cloudLadder authorized-dir + scanCatchUp');
    try {
      this.context.getApplicationContext().setColorMode(ConfigurationConstant.ColorMode.COLOR_MODE_NOT_SET);
      await TokenManager.getInstance().init(this.context);
      await appConfig.init(this.context);
      await userStore.init(this.context);
      await deviceInfoSvc.init(this.context);
      await syncMappingStore.init(this.context);
      // 云梯公共目录（Download/<包名>）用 picker DOWNLOAD 授权，不需要 READ_WRITE_DOWNLOAD_DIRECTORY 权限；
      // 授权须前台，故初始化放在 MainShell.aboutToAppear（见 CloudLadderStorage / initCloudStorage）。
      downloadController.setContext(this.context);
      await downloadStore.init(this.context);
      await downloadController.restorePendingDownloads();
      backgroundTaskHelper.setContext(this.context);
      if (appConfig.forceKeepAliveEnabled) {
        await backgroundTaskHelper.start();
      }
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

