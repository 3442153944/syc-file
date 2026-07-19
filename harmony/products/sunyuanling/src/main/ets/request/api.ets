import { http } from "@kit.NetworkKit";
import { RequestOptions } from "./type/apiType";

export class ApiService {
  private static readonly BASE_URL: string = "http://127.0.0.1:9999";

  /**
   * 核心请求方法
   */
  private static async request<T>(
    path: string,
    method: http.RequestMethod,
    options: RequestOptions
  ): Promise<T> {
    const httpRequest = http.createHttp();
    const fullUrl = path.startsWith('http') ? path : `${ApiService.BASE_URL}${path}`;

    // 显式构建请求参数，避免展开运算符
    const requestConfig: http.HttpRequestOptions = {
      method: method,
      header: {
        'Content-Type': 'application/json'
      },
      expectDataType: http.HttpDataType.STRING, // 明确告知返回字符串，由我们手动解析
      readTimeout: 10000,
      connectTimeout: 10000
    };

    // 显式合并 Header
    if (options.headers) {
      const keys = Object.keys(options.headers);
      for (let i = 0; i < keys.length; i++) {
        const key = keys[i];
        if (requestConfig.header) {
          requestConfig.header[key] = options.headers[key];
        }
      }
    }

    // 只有非 GET 请求才处理 body
    if (method !== http.RequestMethod.GET && options.body) {
      requestConfig.extraData = options.body;
    }

    try {
      const response = await httpRequest.request(fullUrl, requestConfig);

      if (response.responseCode !== http.ResponseCode.OK) {
        throw new Error(`Server Error: ${response.responseCode}`);
      }

      // 关键点：将 string 解析为目标类型 T，使用类型断言
      // 注意：ArkTS 中 JSON.parse() 的结果必须立即断言为具体类型
      const result = JSON.parse(response.result as string) as T;

      return result;
    } catch (err) {
      // 错误处理：确保 throw 的是一个 Error 对象
      if (err instanceof Error) {
        // 如果已经是 Error 类型（包括系统定义的 BusinessError），可以直接抛出
        throw err;
      } else {
        // 如果是其它未知类型，包装成 Error 再抛出
        throw new Error(JSON.stringify(err));
      }
    } finally {
      // 必须释放资源
      httpRequest.destroy();
    }
  }

  // --- 显式方法 ---

  static async get<T>(url: string, headers?: Record<string, string>): Promise<T> {
    return ApiService.request<T>(url, http.RequestMethod.GET, { headers: headers });
  }

  static async post<T>(url: string, bodyObj: Object, headers?: Record<string, string>): Promise<T> {
    // 提前序列化，避免在 request 中处理模糊的 Object
    const bodyString = JSON.stringify(bodyObj);
    return ApiService.request<T>(url, http.RequestMethod.POST, { body: bodyString, headers: headers });
  }
}