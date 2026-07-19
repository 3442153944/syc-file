import http from '@ohos.net.http';
import promptAction from '@ohos.promptAction';
import router from '@ohos.router';

export enum ErrorStrategy {
  MESSAGE,
  NOTIFICATION
}

export interface GlobalConfig {
  showSuccess: boolean;
  showError: boolean;
  errorStrategy: ErrorStrategy;
  rethrow: boolean;
  successMsg?: string;
  errorMsg?: string;
}

export interface ApiResponse<T> {
  code: number;
  data: T;
  message: string;
}

// 对应你代码中的 RequestOptions
export interface RequestOptions extends Partial<GlobalConfig> {
  headers?: Record<string, string>;
}