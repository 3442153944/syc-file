import { backgroundTaskManager } from '@kit.BackgroundTasksKit';
import { notificationManager } from '@kit.NotificationKit';
import { wantAgent, WantAgent } from '@kit.AbilityKit';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';
import { bundleManager } from '@kit.AbilityKit';

/**
 * BackgroundTaskHelper
 * 鸿蒙端保活机制：申请长时任务（continuous task）+ 通知栏常驻。
 * 使用 dataTransfer 模式保持后台存活。
 * 对标 Android service/SyncKeepAliveService.kt。
 */

const KEEP_ALIVE_NOTIFICATION_ID = 9999;

class BackgroundTaskHelperClass {
  private running: boolean = false;
  private ctx: common.UIAbilityContext | null = null;

  setContext(ctx: common.UIAbilityContext): void {
    this.ctx = ctx;
  }

  isRunning(): boolean {
    return this.running;
  }

  async start(): Promise<void> {
    if (this.running || this.ctx === null) return;
    try {
      await this.publishKeepAliveNotification();
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
      await backgroundTaskManager.startBackgroundRunning(
        this.ctx,
        backgroundTaskManager.BackgroundMode.DATA_TRANSFER,
        agent
      );
      this.running = true;
      console.info('[BackgroundTask] started (DATA_TRANSFER)');
    } catch (e) {
      const err = e as BusinessError;
      console.error(`[BackgroundTask] start failed: code=${err.code}, msg=${err.message}`);
    }
  }

  async stop(): Promise<void> {
    if (!this.running || this.ctx === null) return;
    try {
      await backgroundTaskManager.stopBackgroundRunning(this.ctx);
      this.running = false;
      await notificationManager.cancel(KEEP_ALIVE_NOTIFICATION_ID);
      console.info('[BackgroundTask] stopped');
    } catch (e) {
      const err = e as BusinessError;
      console.error(`[BackgroundTask] stop failed: code=${err.code}, msg=${err.message}`);
    }
  }

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
