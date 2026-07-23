import { notificationManager } from '@kit.NotificationKit';
import { BusinessError } from '@kit.BasicServicesKit';

/**
 * NotificationHelper
 * 下载通知栏通知：进度更新 / 完成 / 失败。
 * 对标 Android ui/components/notice/NotificationHelper.kt。
 */

const BASE_ID = 10000;

export class NotificationHelper {
  private static enabled: boolean = true;

  static setEnabled(v: boolean): void {
    NotificationHelper.enabled = v;
  }

  static async notifyProgress(id: number, fileName: string, progress: number, received: number, total: number): Promise<void> {
    if (!NotificationHelper.enabled) return;
    try {
      const request: notificationManager.NotificationRequest = {
        id: BASE_ID + id,
        content: {
          notificationContentType: notificationManager.ContentType.NOTIFICATION_CONTENT_LONG_TEXT,
          longText: {
            title: '正在下载',
            text: fileName,
            additionalText: `${progress}%  ${NotificationHelper.formatBytes(received)} / ${NotificationHelper.formatBytes(total)}`,
            longText: '',
            briefText: '',
            expandedTitle: '',
          },
        },
        isOngoing: true,
      };
      await notificationManager.publish(request);
    } catch (e) {
      console.warn(`[Notification] progress failed: ${JSON.stringify(e)}`);
    }
  }

  static async notifyComplete(id: number, fileName: string): Promise<void> {
    if (!NotificationHelper.enabled) return;
    try {
      const request: notificationManager.NotificationRequest = {
        id: BASE_ID + id,
        content: {
          notificationContentType: notificationManager.ContentType.NOTIFICATION_CONTENT_BASIC_TEXT,
          normal: {
            title: '下载完成',
            text: fileName,
            additionalText: '',
          },
        },
        isOngoing: false,
      };
      await notificationManager.publish(request);
    } catch (e) {
      console.warn(`[Notification] complete failed: ${JSON.stringify(e)}`);
    }
  }

  static async notifyFailed(id: number, fileName: string, error: string): Promise<void> {
    if (!NotificationHelper.enabled) return;
    try {
      const request: notificationManager.NotificationRequest = {
        id: BASE_ID + id,
        content: {
          notificationContentType: notificationManager.ContentType.NOTIFICATION_CONTENT_BASIC_TEXT,
          normal: {
            title: '下载失败',
            text: fileName,
            additionalText: error,
          },
        },
        isOngoing: false,
      };
      await notificationManager.publish(request);
    } catch (e) {
      console.warn(`[Notification] failed failed: ${JSON.stringify(e)}`);
    }
  }

  static async cancel(id: number): Promise<void> {
    try {
      await notificationManager.cancel(BASE_ID + id);
    } catch (e) { /* ignore */ }
  }

  private static formatBytes(bytes: number): string {
    if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`;
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${bytes} B`;
  }
}
