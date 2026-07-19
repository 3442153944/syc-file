import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';

/**
 * DeviceInfo
 * 鸿蒙端设备 ID 生成器（对标 Android DeviceInfoUtil.getDeviceId + 桌面端 device.rs::generate_device_id）。
 * 策略：首次启动生成一次「随机 + 时间戳」字符串，持久化到 preferences，之后稳定复用。
 * 后端 device_id 字段是 string，长度不限；这是稳定性标识，无安全要求。
 *
 * 不依赖 @kit.BasicServicesKit.deviceInfo（需 SystemCapability.Startup.SystemInfo，
 * 不同设备能力不一），保证所有 HarmonyOS 设备均可获取稳定 ID。
 */
const PREF_NAME = 'device_id_store';
const PREF_KEY = 'device_id';

export class DeviceInfoSvc {
  private static inst: DeviceInfoSvc | null = null;
  private cached: string = '';
  private pref: preferences.Preferences | null = null;

  private constructor() {}

  static getInstance(): DeviceInfoSvc {
    if (DeviceInfoSvc.inst === null) {
      DeviceInfoSvc.inst = new DeviceInfoSvc();
    }
    return DeviceInfoSvc.inst;
  }

  async init(context: common.Context): Promise<void> {
    try {
      this.pref = await preferences.getPreferences(context, PREF_NAME);
      const v = await this.pref.get(PREF_KEY, '');
      if (v && (v as string).length > 0) {
        this.cached = v as string;
      } else {
        const id: string = this.generateId();
        await this.pref.put(PREF_KEY, id);
        await this.pref.flush();
        this.cached = id;
      }
    } catch (err) {
      const e = err as BusinessError;
      console.error(`[DeviceInfo] init failed: ${e.code} ${e.message}`);
      if (this.cached.length === 0) {
        this.cached = this.generateId();
      }
    }
  }

  getDeviceId(): string {
    if (this.cached.length === 0) {
      this.cached = this.generateId();
    }
    return this.cached;
  }

  /** 生成 32 字符 hex（时间戳 + 随机数，无安全要求） */
  private generateId(): string {
    const ts: string = Date.now().toString(16).padStart(13, '0');
    const rand: string = Math.floor(Math.random() * 0xFFFFFFFF).toString(16).padStart(8, '0');
    const rand2: string = Math.floor(Math.random() * 0xFFFFFFFF).toString(16).padStart(8, '0');
    return `${ts}${rand}${rand2}`.substring(0, 32).padEnd(32, '0');
  }
}

export const deviceInfoSvc = DeviceInfoSvc.getInstance();
