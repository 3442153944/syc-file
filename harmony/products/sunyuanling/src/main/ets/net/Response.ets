/**
 * 统一响应模型
 * 后端响应信封：{ code, message, data }，code == 200 表示成功。
 * 注意：code 与 HTTP 状态码语义对齐，但服务端在 RequireAuth 中间件失败时
 * 仍返回 HTTP 200 但 body.code = 401。
 */

export interface ApiResponseData<T> {
  code: number;
  message: string;
  data: T | null;
}

/** 业务信封别名，对标 Android Response<T> */
export type ApiResponse<T> = ApiResponseData<T>;

/**
 * 分页响应结构（后端 ListTasks 在带 page&page_size 时返回）。
 */
export interface PageData<T> {
  list: T[];
  total: number;
  page: number;
  page_size: number;
  total_pages: number;
}

/**
 * 响应异常类
 */
export class ResponseException extends Error {
  code: number;

  constructor(code: number, message: string) {
    super(`[${code}] ${message}`);
    this.code = code;
    this.name = 'ResponseException';
  }

  isUnauthorized(): boolean { return this.code === 401; }
  isForbidden(): boolean { return this.code === 403; }
  isNotFound(): boolean { return this.code === 404; }
  isBadRequest(): boolean { return this.code === 400; }
  isServerError(): boolean { return this.code >= 500 && this.code < 600; }
}

/**
 * 分片上传相关异常
 */
export class ChunkVerifyException extends Error {
  index: number;

  constructor(index: number, message: string) {
    super(`chunk ${index} verify failed: ${message}`);
    this.index = index;
    this.name = 'ChunkVerifyException';
  }
}

export class SessionGoneException extends Error {
  constructor(message: string) {
    super(`session gone: ${message}`);
    this.name = 'SessionGoneException';
  }
}
