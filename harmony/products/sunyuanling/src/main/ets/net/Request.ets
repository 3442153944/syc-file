import http from '@ohos.net.http';
import { appConfig } from '../app/AppConfig';
import { authManager } from './AuthManager';
import { TokenManager } from '../token/tokenManager';
import { ApiResponseData, ResponseException, SessionGoneException, ChunkVerifyException } from './Response';

/**
 * RequestOptions —— 请求方法参数对象接口（ArkTS 要求对象字面量必须有对应接口/类）。
 */
export interface RequestOptions {
  queryParams?: Record<string, string>;
  body?: Object | null;
  headers?: Record<string, string>;
  bodyIsRawString?: boolean;
}

/** 下载探测结果 */
export interface DownloadProbe {
  totalBytes: number;
  acceptRanges: boolean;
}

/**
 * Request —— 鸿蒙端统一 HTTP 网络单例。
 * 对标 Android network/request.kt：
 *  - baseUrl 动态读取自 AppConfig（避免 Android 端「单例首次求值的陈旧 URL bug」）
 *  - 请求自动注入 Token 头
 *  - 业务信封 { code, message, data } 校验，code != 200 抛 ResponseException
 *  - HTTP 401 或业务 code 401：清 token + 广播 TOKEN_EXPIRED
 *  - 自动读取响应头 New-Token 静默续期
 */
export class Request {
  private static inst: Request | null = null;

  static getInstance(): Request {
    if (Request.inst === null) {
      Request.inst = new Request();
    }
    return Request.inst;
  }

  private constructor() {}

  private get baseUrl(): string {
    return `${appConfig.getBaseUrl()}/v1`;
  }

  /**
   * 静态资源 URL（头像等），对标 Android `Request.baseStaticUrl`。
   *
   * ⚠ 服务端存的 `user.avatar` 已经是**含目录的相对路径**（`static/avatar/avatar_1_x.png`，
   *   见 config.yaml `user.avatar_path`），所以这里**只拼服务器根，不能再补 `/static/`**——
   *   补了就变成 `.../static/static/avatar/x.png`，必然 404，头像永远出不来。
   *   另外静态资源不走 `/v1`（那是 API 前缀）。
   */
  static staticUrl(relPath: string): string {
    if (relPath.length === 0) return '';
    if (relPath.startsWith('http')) return relPath;
    const rel: string = relPath.replace(/^\/+/, '');
    return `${appConfig.getBaseUrl()}/${rel}`;
  }

  private buildUrl(endpoint: string, queryParams?: Record<string, string>): string {
    let url = endpoint.startsWith('http') ? endpoint : `${this.baseUrl}${endpoint}`;
    if (queryParams !== undefined && queryParams !== null) {
      const keys = Object.keys(queryParams);
      if (keys.length > 0) {
        const parts: string[] = [];
        for (let i = 0; i < keys.length; i++) {
          const k = keys[i];
          parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(queryParams[k])}`);
        }
        url += (url.includes('?') ? '&' : '?') + parts.join('&');
      }
    }
    return url;
  }

  /** 构造带 Token 的请求头（手动合并，ArkTS 不允许 spread） */
  private buildHeaders(extra?: Record<string, string>): Record<string, string> {
    const header: Record<string, string> = {
      'Content-Type': 'application/json',
    };
    const token = TokenManager.getInstance().getToken();
    if (token.length > 0) {
      header['Token'] = token;
    }
    if (extra !== undefined && extra !== null) {
      const keys = Object.keys(extra);
      for (let i = 0; i < keys.length; i++) {
        const k = keys[i];
        header[k] = extra[k];
      }
    }
    return header;
  }

  private safeDestroy(req: http.HttpRequest): void {
    try { req.destroy(); } catch (e) { /* ignore */ }
  }

  /** 处理 New-Token 响应头（静默续期） */
  private async tryRefreshTokenFromHeader(resp: http.HttpResponse): Promise<void> {
    try {
      const headers = resp.header as Record<string, string>;
      if (headers === null || headers === undefined) return;
      const keys = Object.keys(headers);
      for (let i = 0; i < keys.length; i++) {
        const k = keys[i];
        if (k.toLowerCase() === 'new-token') {
          const newToken = headers[k];
          if (newToken !== null && newToken !== undefined && newToken.length > 0) {
            await TokenManager.getInstance().setToken(newToken);
            console.info('[Request] token refreshed via New-Token header');
          }
        }
      }
    } catch (e) { /* ignore */ }
  }

  /**
   * 核心请求方法：发送请求并解析业务信封。
   * 成功返回「完整信封」按 T 类型反序列化的结果（不脱壳，对标 Android request.kt 行为）。
   * 业务 code != 200 抛 ResponseException。
   */
  private async requestJson<T>(
    method: http.RequestMethod,
    endpoint: string,
    options: RequestOptions
  ): Promise<T> {
    const url = this.buildUrl(endpoint, options.queryParams);
    const header = this.buildHeaders(options.headers);
    let extraData: string | undefined;
    if (method !== http.RequestMethod.GET && options.body !== null && options.body !== undefined) {
      if (options.bodyIsRawString === true) {
        extraData = options.body as string;
      } else {
        extraData = JSON.stringify(options.body);
      }
    }

    const httpRequest = http.createHttp();
    try {
      const reqOpts: http.HttpRequestOptions = {
        method,
        header,
        extraData,
        expectDataType: http.HttpDataType.STRING,
        connectTimeout: appConfig.connectTimeoutMs,
        readTimeout: appConfig.readTimeoutMs,
      };
      if (appConfig.caPath.length > 0) {
        reqOpts.caPath = appConfig.caPath;
      }
      const response = await httpRequest.request(url, reqOpts);

      await this.tryRefreshTokenFromHeader(response);

      if (response.responseCode === 401) {
        await TokenManager.getInstance().clear();
        authManager.notifyTokenExpired();
        throw new ResponseException(401, '认证失败');
      }
      if (response.responseCode !== 200) {
        throw new ResponseException(response.responseCode, `HTTP ${response.responseCode}`);
      }

      const text = response.result as string;
      if (text === null || text.length === 0) {
        throw new ResponseException(500, '响应体为空');
      }

      const obj = JSON.parse(text) as ApiResponseData<T>;
      if (obj.code === 200 || obj.code === 0 || obj.code === undefined) {
        return obj as T;
      }
      if (obj.code === 401) {
        await TokenManager.getInstance().clear();
        authManager.notifyTokenExpired();
        throw new ResponseException(401, obj.message);
      }
      throw new ResponseException(obj.code, obj.message);
    } catch (err) {
      if (err instanceof ResponseException) throw err;
      const msg = (err as Error)?.message ?? JSON.stringify(err);
      throw new ResponseException(-1, msg);
    } finally {
      this.safeDestroy(httpRequest);
    }
  }

  // ====== 公开方法 ======

  static async get<T>(endpoint: string, queryParams?: Record<string, string>): Promise<T> {
    const opts: RequestOptions = { queryParams };
    return Request.getInstance().requestJson<T>(http.RequestMethod.GET, endpoint, opts);
  }

  static async post<T>(endpoint: string, body?: Object): Promise<T> {
    const opts: RequestOptions = { body };
    return Request.getInstance().requestJson<T>(http.RequestMethod.POST, endpoint, opts);
  }

  static async put<T>(endpoint: string, body?: Object): Promise<T> {
    const opts: RequestOptions = { body };
    return Request.getInstance().requestJson<T>(http.RequestMethod.PUT, endpoint, opts);
  }

  static async delete<T>(endpoint: string, body?: Object): Promise<T> {
    const opts: RequestOptions = { body };
    return Request.getInstance().requestJson<T>(http.RequestMethod.DELETE, endpoint, opts);
  }

  /**
   * 分片上传单分片：POST application/octet-stream，query 带 upload_id + index。
   *  - code 200 → 成功（返回 UploadChunkData）
   *  - code 422 → ChunkVerifyException
   *  - code 404 → SessionGoneException
   */
  static async uploadChunk(
    endpoint: string,
    uploadId: string,
    index: number,
    data: Uint8Array
  ): Promise<Object> {
    const query: Record<string, string> = {
      'upload_id': uploadId,
      'index': String(index),
    };
    const url = Request.getInstance().buildUrl(endpoint, query);
    const header = Request.getInstance().buildHeaders({
      'Content-Type': 'application/octet-stream',
    });
    const httpRequest = http.createHttp();
    try {
      const reqOpts: http.HttpRequestOptions = {
        method: http.RequestMethod.POST,
        header,
        extraData: data.buffer,
        expectDataType: http.HttpDataType.STRING,
        connectTimeout: appConfig.connectTimeoutMs,
        readTimeout: appConfig.readTimeoutMs,
      };
      if (appConfig.caPath.length > 0) {
        reqOpts.caPath = appConfig.caPath;
      }
      const response = await httpRequest.request(url, reqOpts);
      await Request.getInstance().tryRefreshTokenFromHeader(response);
      const text = response.result as string;
      const obj = JSON.parse(text) as ApiResponseData<Object>;
      if (obj.code === 200) return obj.data as Object;
      if (obj.code === 422) throw new ChunkVerifyException(index, obj.message);
      if (obj.code === 404) throw new SessionGoneException(obj.message);
      throw new ResponseException(obj.code, obj.message);
    } catch (err) {
      if (err instanceof ResponseException) throw err;
      if (err instanceof SessionGoneException) throw err;
      if (err instanceof ChunkVerifyException) throw err;
      throw new ResponseException(-1, (err as Error)?.message ?? JSON.stringify(err));
    } finally {
      Request.getInstance().safeDestroy(httpRequest);
    }
  }

  /**
   * 文件下载：GET，返回 ArrayBuffer（二进制流）。token 注入 query string。
   */
  static async downloadBuffer(
    endpoint: string,
    queryParams: Record<string, string>
  ): Promise<ArrayBuffer> {
    const params: Record<string, string> = {};
    const qKeys = Object.keys(queryParams);
    for (let i = 0; i < qKeys.length; i++) {
      const k = qKeys[i];
      params[k] = queryParams[k];
    }
    const token = TokenManager.getInstance().getToken();
    if (token.length > 0) {
      params['token'] = token;
    }
    const url = Request.getInstance().buildUrl(endpoint, params);
    const header: Record<string, string> = {};
    if (token.length > 0) {
      header['Token'] = token;
    }
    const httpRequest = http.createHttp();
    try {
      const reqOpts: http.HttpRequestOptions = {
        method: http.RequestMethod.GET,
        header,
        expectDataType: http.HttpDataType.ARRAY_BUFFER,
        connectTimeout: appConfig.connectTimeoutMs,
        readTimeout: 0,
      };
      if (appConfig.caPath.length > 0) {
        reqOpts.caPath = appConfig.caPath;
      }
      const response = await httpRequest.request(url, reqOpts);
      if (response.responseCode !== 200 && response.responseCode !== 206) {
        throw new ResponseException(response.responseCode, `HTTP ${response.responseCode}`);
      }
      return response.result as ArrayBuffer;
    } finally {
      Request.getInstance().safeDestroy(httpRequest);
    }
  }

  /**
   * Range 下载：返回指定字节区间的 ArrayBuffer，支持断点续传。
   */
  static async downloadRange(
    endpoint: string,
    queryParams: Record<string, string>,
    start: number,
    end: number
  ): Promise<ArrayBuffer> {
    const params: Record<string, string> = {};
    const qKeys = Object.keys(queryParams);
    for (let i = 0; i < qKeys.length; i++) {
      const k = qKeys[i];
      params[k] = queryParams[k];
    }
    const token = TokenManager.getInstance().getToken();
    if (token.length > 0) {
      params['token'] = token;
    }
    const url = Request.getInstance().buildUrl(endpoint, params);
    const header: Record<string, string> = {
      'Range': `bytes=${start}-${end}`,
    };
    if (token.length > 0) {
      header['Token'] = token;
    }
    const httpRequest = http.createHttp();
    try {
      const reqOpts: http.HttpRequestOptions = {
        method: http.RequestMethod.GET,
        header,
        expectDataType: http.HttpDataType.ARRAY_BUFFER,
        connectTimeout: appConfig.connectTimeoutMs,
        readTimeout: 0,
      };
      if (appConfig.caPath.length > 0) {
        reqOpts.caPath = appConfig.caPath;
      }
      const response = await httpRequest.request(url, reqOpts);
      if (response.responseCode !== 200 && response.responseCode !== 206) {
        throw new ResponseException(response.responseCode, `HTTP ${response.responseCode}`);
      }
      return response.result as ArrayBuffer;
    } finally {
      Request.getInstance().safeDestroy(httpRequest);
    }
  }

  /**
   * multipart/form-data POST（用于 update-info 等需要表单+文件的接口）。
   * fields: 文本字段 key-value；fileField/fileData/mimeType/fileName: 可选文件部分。
   */
  static async postMultipart<T>(
    endpoint: string,
    fields: Record<string, string>,
    fileField?: string,
    fileData?: ArrayBuffer,
    mimeType?: string,
    fileName?: string
  ): Promise<T> {
    const boundary = `----FormBoundary${Date.now().toString(36)}${Math.random().toString(36).substring(2)}`;
    const parts: string[] = [];
    const fieldKeys = Object.keys(fields);
    for (let i = 0; i < fieldKeys.length; i++) {
      const k = fieldKeys[i];
      parts.push(`--${boundary}\r\nContent-Disposition: form-data; name="${k}"\r\n\r\n${fields[k]}\r\n`);
    }
    let fileHeader = '';
    if (fileField !== undefined && fileData !== undefined) {
      const fn = fileName !== undefined ? fileName : 'file';
      const mt = mimeType !== undefined ? mimeType : 'application/octet-stream';
      fileHeader = `--${boundary}\r\nContent-Disposition: form-data; name="${fileField}"; filename="${fn}"\r\nContent-Type: ${mt}\r\n\r\n`;
    }
    const closing = `\r\n--${boundary}--\r\n`;

    let bodyBuffer: ArrayBuffer;
    if (fileHeader.length > 0 && fileData !== undefined) {
      const headStr = parts.join('') + fileHeader;
      const headBuf = new Uint8Array(headStr.split('').map((c: string) => c.charCodeAt(0)));
      const tailBuf = new Uint8Array(closing.split('').map((c: string) => c.charCodeAt(0)));
      const fileBytes = new Uint8Array(fileData);
      bodyBuffer = new ArrayBuffer(headBuf.length + fileBytes.length + tailBuf.length);
      const merged = new Uint8Array(bodyBuffer);
      merged.set(headBuf, 0);
      merged.set(fileBytes, headBuf.length);
      merged.set(tailBuf, headBuf.length + fileBytes.length);
    } else {
      const fullStr = parts.join('') + `--${boundary}--\r\n`;
      const strCodes = fullStr.split('').map((c: string) => c.charCodeAt(0));
      bodyBuffer = new ArrayBuffer(strCodes.length);
      const arr = new Uint8Array(bodyBuffer);
      for (let i = 0; i < strCodes.length; i++) {
        arr[i] = strCodes[i];
      }
    }

    const url = Request.getInstance().buildUrl(endpoint);
    const header = Request.getInstance().buildHeaders({
      'Content-Type': `multipart/form-data; boundary=${boundary}`,
    });
    const httpRequest = http.createHttp();
    try {
      const reqOpts: http.HttpRequestOptions = {
        method: http.RequestMethod.POST,
        header,
        extraData: bodyBuffer,
        expectDataType: http.HttpDataType.STRING,
        connectTimeout: appConfig.connectTimeoutMs,
        readTimeout: appConfig.readTimeoutMs,
      };
      if (appConfig.caPath.length > 0) {
        reqOpts.caPath = appConfig.caPath;
      }
      const response = await httpRequest.request(url, reqOpts);
      await Request.getInstance().tryRefreshTokenFromHeader(response);
      if (response.responseCode === 401) {
        await TokenManager.getInstance().clear();
        authManager.notifyTokenExpired();
        throw new ResponseException(401, '认证失败');
      }
      if (response.responseCode !== 200) {
        throw new ResponseException(response.responseCode, `HTTP ${response.responseCode}`);
      }
      const text = response.result as string;
      const obj = JSON.parse(text) as ApiResponseData<T>;
      if (obj.code === 200 || obj.code === 0 || obj.code === undefined) {
        return obj as T;
      }
      if (obj.code === 401) {
        await TokenManager.getInstance().clear();
        authManager.notifyTokenExpired();
        throw new ResponseException(401, obj.message);
      }
      throw new ResponseException(obj.code, obj.message);
    } catch (err) {
      if (err instanceof ResponseException) throw err;
      throw new ResponseException(-1, (err as Error)?.message ?? JSON.stringify(err));
    } finally {
      Request.getInstance().safeDestroy(httpRequest);
    }
  }

  /**
   * 探测下载响应头（Content-Length / Accept-Ranges）以支持断点续传规划。
   */
  static async probeDownload(
    endpoint: string,
    queryParams: Record<string, string>
  ): Promise<DownloadProbe> {
    const params: Record<string, string> = {};
    const qKeys = Object.keys(queryParams);
    for (let i = 0; i < qKeys.length; i++) {
      const k = qKeys[i];
      params[k] = queryParams[k];
    }
    const token = TokenManager.getInstance().getToken();
    if (token.length > 0) {
      params['token'] = token;
    }
    const url = Request.getInstance().buildUrl(endpoint, params);
    const header: Record<string, string> = {
      'Range': 'bytes=0-0',
    };
    if (token.length > 0) {
      header['Token'] = token;
    }
    const httpRequest = http.createHttp();
    try {
      const reqOpts: http.HttpRequestOptions = {
        method: http.RequestMethod.GET,
        header,
        expectDataType: http.HttpDataType.ARRAY_BUFFER,
        connectTimeout: appConfig.connectTimeoutMs,
        readTimeout: 0,
      };
      if (appConfig.caPath.length > 0) {
        reqOpts.caPath = appConfig.caPath;
      }
      const response = await httpRequest.request(url, reqOpts);
      if (response.responseCode !== 200 && response.responseCode !== 206) {
        throw new ResponseException(response.responseCode, `HTTP ${response.responseCode}`);
      }
      const headers = response.header as Record<string, string>;
      let total = 0;
      let acceptRanges = false;
      if (headers !== null && headers !== undefined) {
        const keys = Object.keys(headers);
        for (let i = 0; i < keys.length; i++) {
          const k = keys[i];
          const lower = k.toLowerCase();
          const v = headers[k];
          if (lower === 'content-range') {
            const m = /\/(\d+)/.exec(v);
            if (m !== null) {
              total = Number(m[1]);
              acceptRanges = true;
            }
          } else if (lower === 'content-length' && total === 0) {
            total = Number(v);
          } else if (lower === 'accept-ranges') {
            acceptRanges = v.toLowerCase() === 'bytes';
          }
        }
      }
      const result: DownloadProbe = { totalBytes: total, acceptRanges };
      return result;
    } finally {
      Request.getInstance().safeDestroy(httpRequest);
    }
  }
}
