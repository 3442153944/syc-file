import { backgroundTaskManager } from '@kit.BackgroundTasksKit';
import { notificationManager } from '@kit.NotificationKit';
import { wantAgent, WantAgent } from '@kit.AbilityKit';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';
import { bundleManager } from '@kit.AbilityKit';

/**
 * BackgroundTaskHelper
 * 鸿蒙端保活：申请 DATA_TRANSFER 长时任务，让应用退到后台后仍能联网继续同步。
 * 对标 Android service/SyncKeepAliveService.kt。
 *
 * ⚠ 没有长时任务，应用退到后台几秒后进程被系统冻结：在途 HTTP 连接被掐、WS 断开，
 *   而且冻结后连 setTimeout 重连都不会跑，表现就是「同步断了再也不继续」。
 *
 * ── 三个必要条件，缺一不可（挨个踩过） ──────────────────────────
 *  1. **module.json5 的 ability 里声明 `backgroundModes: ["dataTransfer"]`**。
 *     这是最容易漏的一条：不声明，startBackgroundRunning 一定失败，
 *     且跟通知授权、前后台时机全无关系。运行时申请的模式必须是声明的子集。
 *  2. **`ohos.permission.KEEP_BACKGROUND_RUNNING`** 权限（module.json5）。
 *  3. **必须在应用前台时调**。onCreate 阶段窗口还没起来，系统以 9800005 拒绝，
 *     所以入口在 Ability.onForeground，做成幂等可重试的 ensureStarted()。
 *
 * ⚠ 别把自建通知 await 在 startBackgroundRunning 前面：应用没申请过通知授权时
 *   notificationManager.publish() 抛 1600004，会被同一个 catch 吞掉，长时任务根本没申请到。
 *   长时任务成功后系统自带一条常驻通知，自建通知只是补充信息，失败无所谓。
 *
 * 排查：hilog 过滤「云梯Boot」；设置页「保活状态」显示运行状态或失败码。
 */

const KEEP_ALIVE_NOTIFICATION_ID = 9999;
/** AppStorage key：长时任务是否在跑，供 UI 展示（避免再次静默失败） */
export const KEEPALIVE_STATE_KEY = 'sync_keepalive_on';

class BackgroundTaskHelperClass {
  private running: boolean = false;
  private ctx: common.UIAbilityContext | null = null;
  private starting: boolean = false;
  private lastError: string = '';

  setContext(ctx: common.UIAbilityContext): void {
    this.ctx = ctx;
  }

  isRunning(): boolean {
    return this.running;
  }

  getLastError(): string {
    return this.lastError;
  }

  /**
   * 幂等申请长时任务。可以在每次 onForeground 调用——已在跑就直接返回。
   * 之所以要反复调：系统可能在某些场景回收长时任务，重进前台是最好的重试时机。
   */
  async ensureStarted(): Promise<boolean> {
    if (this.running) return true;
    if (this.starting) return false;
    if (this.ctx === null) {
      this.lastError = 'context 未设置';
      return false;
    }
    this.starting = true;
    try {
      const bundleInfo = await bundleManager.getBundleInfoForSelf(
        bundleManager.BundleFlag.GET_BUNDLE_INFO_DEFAULT
      );
      const wantAgentInfo: wantAgent.WantAgentInfo = {
        wants: [{
          bundleName: bundleInfo.name,
          abilityName: 'SunyuanlingAbility',
        }],
        operationType: wantAgent.OperationType.START_ABILITY,
        requestCode: 0,
        wantAgentFlags: [wantAgent.WantAgentFlags.UPDATE_PRESENT_FLAG],
      };
      const agent: WantAgent = await wantAgent.getWantAgent(wantAgentInfo);
      // 关键一步：先把长时任务拿到手（模式须与 module.json5 的 backgroundModes 对应）
      await backgroundTaskManager.startBackgroundRunning(
        this.ctx,
        backgroundTaskManager.BackgroundMode.DATA_TRANSFER,
        agent
      );
      this.running = true;
      this.lastError = '';
      AppStorage.setOrCreate<boolean>(KEEPALIVE_STATE_KEY, true);
      console.info('[云梯Boot] BackgroundTask started (DATA_TRANSFER) —— 后台可继续联网');
    } catch (e) {
      const err = e as BusinessError;
      this.lastError = `code=${err.code} msg=${err.message}`;
      AppStorage.setOrCreate<boolean>(KEEPALIVE_STATE_KEY, false);
      // 9800005 = 后台作业校验失败：先查 module.json5 的 backgroundModes 是否声明了 dataTransfer，
      //           再查是否真的在前台调用；201 = 缺 KEEP_BACKGROUND_RUNNING 权限
      console.error(`[云梯Boot] BackgroundTask start FAILED: ${this.lastError} —— 后台会被冻结，同步必断`);
      return false;
    } finally {
      this.starting = false;
    }

    // 通知是可选的补充信息：放在长时任务之后，且失败不影响保活
    this.publishKeepAliveNotification().catch((e: Error) => {
      console.warn(`[BackgroundTask] keep-alive notification skipped: ${e.message}`);
    });
    return true;
  }

  /** 兼容旧调用名。 */
  async start(): Promise<void> {
    await this.ensureStarted();
  }

  async stop(): Promise<void> {
    if (!this.running || this.ctx === null) return;
    try {
      await backgroundTaskManager.stopBackgroundRunning(this.ctx);
      this.running = false;
      AppStorage.setOrCreate<boolean>(KEEPALIVE_STATE_KEY, false);
      try { await notificationManager.cancel(KEEP_ALIVE_NOTIFICATION_ID); } catch (e) { /* ignore */ }
      console.info('[BackgroundTask] stopped');
    } catch (e) {
      const err = e as BusinessError;
      console.error(`[BackgroundTask] stop failed: code=${err.code}, msg=${err.message}`);
    }
  }

  /**
   * 申请通知授权（须前台 + UIAbilityContext）。
   * 只影响自建通知与下载进度通知的可见性，**不影响长时任务保活**。
   */
  async requestNotificationPermission(): Promise<boolean> {
    if (this.ctx === null) return false;
    try {
      const enabled: boolean = await notificationManager.isNotificationEnabled();
      if (enabled) return true;
      await notificationManager.requestEnableNotification(this.ctx);
      return true;
    } catch (e) {
      const err = e as BusinessError;
      console.warn(`[BackgroundTask] requestEnableNotification failed: code=${err.code} msg=${err.message}`);
      return false;
    }
  }

  /**
   * 自建通知，纯补充信息。长时任务申请成功后系统会自带一条常驻通知，
   * 所以这里不用也不该去折腾 slot——`NotificationRequest` 的 slot 字段引用的是
   * 另一个 `SlotType` 声明，直接写 `notificationManager.SlotType.*` 编译不过（10505001）。
   */
  private async publishKeepAliveNotification(): Promise<void> {
    const request: notificationManager.NotificationRequest = {
      id: KEEP_ALIVE_NOTIFICATION_ID,
      content: {
        notificationContentType: notificationManager.ContentType.NOTIFICATION_CONTENT_BASIC_TEXT,
        normal: {
          title: '云梯同步服务',
          text: '文件同步服务运行中',
          additionalText: '',
        },
      },
      isOngoing: true,
    };
    await notificationManager.publish(request);
  }
}

export const backgroundTaskHelper = new BackgroundTaskHelperClass();
