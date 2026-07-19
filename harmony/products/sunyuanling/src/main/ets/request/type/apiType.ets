import http from '@ohos.net.http';
import { BusinessError } from '@ohos.base';

/**
 * 基础响应结构
 */
export interface BaseResponse<T> {
  code: number;
  data: T;
  message: string;
}

/**
 * 请求配置接口
 */
export interface RequestOptions {
  headers?: Record<string, string>;
  // 在 ArkTS 中，body 通常序列化为 string 传递给 http 模块
  body?: string;
}