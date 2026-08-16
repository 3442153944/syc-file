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
import { syncBaseStore } from '../sync/SyncBaseStore';
import { backgroundTaskHelper } from '../service/BackgroundTaskHelper';
import { syncEngine } from '../sync/SyncEngine';
import { cloudLadderStorage, initCloudStorage } from '../storage/CloudLadderStorage';
import { syncStagingStore } from '../storage/SyncStagingStore';
import { flushLog } from '../storage/FlushLog';

const DOMAIN = 0x0000;

export default class SunyuanlingAbility extends UIAbility {
  async onCreate(): Promise<void> {
    // 启动探针：真机 hilog 过滤 "云梯Boot" 即可确认本版本代码是否真正冷启动生效
    hilog.info(DOMAIN, '云梯Boot', 'onCreate v7: relative_path 含文件名修正 + keep-alive 前台申请 + 沙盒暂存');
    try {
      this.context.getApplicationContext().setColorMode(ConfigurationConstant.ColorMode.COLOR_MODE_NOT_SET);
      await TokenManager.getInstance().init(this.context);
      await appConfig.init(this.context);
      await userStore.init(this.context);
      await deviceInfoSvc.init(this.context);
      await syncMappingStore.init(this.context);
      // 同步基线台账：scan 的清单以它为准（授权目录上 fs.listFile 不可靠）
      await syncBaseStore.init(this.context);
      // 云梯公共目录（Download/<包名>）用 picker DOWNLOAD 授权，不需要 READ_WRITE_DOWNLOAD_DIRECTORY 权限；
      // 授权须前台，故初始化放在 MainShell.aboutToAppear（见 CloudLadderStorage / initCloudStorage）。
      // 沙盒暂存区：后台同步（公共目录授权已失效）时的落盘兜底，回前台由 onForeground 搬运。
      await syncStagingStore.init(this.context);
      await flushLog.init(this.context);
      // 注入 UIAbilityContext：授权失效时 ensureWritable() 可在前台**就地**重取，
      // 不必干等下一次前后台切换（否则一次写失败会把整个会话钉死在沙盒里）。
      cloudLadderStorage.setContext(this.context);
      downloadController.setContext(this.context);
      await downloadStore.init(this.context);
      await downloadController.restorePendingDownloads();
      // ⚠ 只设置 context，**不在这里 start**：onCreate 阶段窗口还没起来、应用不算前台，
      //   startBackgroundRunning 会被系统以 9800005 拒绝。实际申请放在 onForeground。
      backgroundTaskHelper.setContext(this.context);
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
    // 串行一条链：共享目录 → 归位 → 追赶 → 保活。绝不并发（见 onForegroundTasks 注释）。
    this.onForegroundTasks().catch((e: Error) => {
      hilog.warn(DOMAIN, '云梯Boot', 'foreground tasks failed: %{public}s', e.message);
    });
  }

  /**
   * 幂等申请长时任务。每次回前台都试一次——已在跑会直接返回。
   *
   * 前置条件见 BackgroundTaskHelper 类注释：module.json5 必须声明 backgroundModes，
   * 且必须在前台调用，否则 9800005。
   */
  private async ensureKeepAlive(): Promise<void> {
    if (!appConfig.autoSyncEnabled && !appConfig.forceKeepAliveEnabled) return;
    const ok: boolean = await backgroundTaskHelper.ensureStarted();
    if (ok) {
      hilog.info(DOMAIN, '云梯Boot', 'keep-alive running (DATA_TRANSFER)');
      // 保活拿到了才申请通知授权：系统常驻通知受应用通知总开关管
      await backgroundTaskHelper.requestNotificationPermission();
    } else {
      hilog.error(DOMAIN, '云梯Boot', 'keep-alive NOT running: %{public}s', backgroundTaskHelper.getLastError());
    }
  }

  onBackground(): void {
    hilog.info(DOMAIN, 'testTag', '%{public}s', 'Ability onBackground');
    // 退后台随时可能被冻结/回收：把基线台账防抖窗口里的账立刻落盘。
    // 台账少一截 = 下次上线清单少一截 = 服务端把差的那截重派一遍。
    syncBaseStore.flushNow().catch((e: Error) => {
      hilog.warn(DOMAIN, '云梯Boot', 'baseline flush failed: %{public}s', e.message);
    });
    // 这里**不要**去标记「不可写」。系统弹框（通知/定位授权、picker 自身）也会触发 onBackground，
    // 一标记就会把前台的正常写盘误判成不可写，整轮落沙盒。能不能写交给 fs.open 的结果说话。
  }

  /**
   * 前台归位流程：拿公共目录授权 → flush 沙盒暂存 → 触发一轮追赶 → 最后才碰保活。
   *
   * ⚠ 顺序不能反、更不能并发：保活会弹通知/定位授权框，弹框期间 Ability 会走 onBackground，
   *   之前把两条链并发 fire，弹框把前台状态打翻，导致前台也在往沙盒写。
   *   共享目录是用户看得见的东西，优先保证它；保活失败最多影响后台，不影响前台可用。
   */
  private async onForegroundTasks(): Promise<void> {
    cloudLadderStorage.onForegroundHint();
    if (!syncEngine.isStarted()) return;

    // 1) 先把共享目录搞定（用户可见的东西优先）
    const ready: boolean = await initCloudStorage(this.context);
    if (ready) {
      const moved: number = await syncEngine.flushStaging();
      hilog.info(DOMAIN, '云梯Boot', 'foreground flush moved=%{public}d', moved);
      // 没搬干净的话 flushStaging 内部已挂上 5s 重试表，这里不用再操心
      syncEngine.triggerCatchUp();
    } else {
      hilog.warn(DOMAIN, '云梯Boot', 'foreground: cloud dir not authorized');
    }

    // 2) 共享目录处理完，才去申请保活（可能弹框）
    await this.ensureKeepAlive();
  }
}

