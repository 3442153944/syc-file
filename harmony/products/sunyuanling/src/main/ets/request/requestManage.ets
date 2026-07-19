import http from '@ohos.net.http';
import { TokenManager } from '../token/tokenManager';

export interface ApiResponse<T> {
  code: number;
  data: T;
  message: string;
}

export class RequestService {
  private static readonly BASE_URL: string = "http://127.0.0.1:9999";

  /**
   * 核心请求方法：手动处理所有 ArkTS 限制
   */
  private static async request<T>(
    path: string,
    method: http.RequestMethod,
    data?: Object
  ): Promise<T> {
    const httpRequest = http.createHttp();
    const url = path.startsWith('http') ? path : `${RequestService.BASE_URL}${path}`;

    // 1. 获取 Token
    const token = TokenManager.getInstance().getToken();

    // 2. 构造 Header (ArkTS 不允许 spread，必须手动赋值)
    const header: Record<string, string> = {
      'Content-Type': 'application/json'
    };
    if (token !== "") {
      header['Token'] = token;
    }

    try {
      const response = await httpRequest.request(url, {
        method: method,
        header: header,
        extraData: (method !== http.RequestMethod.GET && data) ? JSON.stringify(data) : undefined,
        expectDataType: http.HttpDataType.STRING,
        readTimeout: 10000
      });

      if (response.responseCode !== http.ResponseCode.OK) {
        throw new Error(`Network Error: ${response.responseCode}`);
      }

      // 3. 业务逻辑解析
      const res = JSON.parse(response.result as string) as ApiResponse<T>;

      if (res.code === 200 || res.code === 0) {
        return res.data;
      }

      // 401 逻辑：这里只负责清理 Token 并抛出错误，不负责跳转
      if (res.code === 401) {
        TokenManager.getInstance().clear();
        throw new Error("AUTH_FAILED");
      }

      throw new Error(res.message || "Unknown Business Error");

    } catch (err) {
      if (err instanceof Error) throw err;
      throw new Error("UNKNOWN_ERROR");
    } finally {
      httpRequest.destroy(); // 必须手动销毁
    }
  }

  // 暴露给外部的显式方法
  static async get<T>(path: string): Promise<T> {
    return RequestService.request<T>(path, http.RequestMethod.GET);
  }

  static async post<T>(path: string, data: Object): Promise<T> {
    return RequestService.request<T>(path, http.RequestMethod.POST, data);
  }
}