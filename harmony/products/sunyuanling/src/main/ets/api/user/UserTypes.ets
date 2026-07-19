/**
 * UserTypes
 * 用户域请求/响应类型。字段对齐后端 Go struct json tag。
 */

/** 通用用户信息（对应后端 model.User） */
export interface UserInfo {
  id: number;
  username: string;
  email: string | null;
  phone: string | null;
  avatar: string | null;
  role: string | null;
  status: number;
  last_login: string | null;
  created_at: string | null;
}

/** 登录请求参数 */
export interface LoginParams {
  username?: string;
  email?: string;
  phone?: string;
  password: string;
}

/** 登录响应 data 字段 */
export interface LoginData {
  token: string;
  user: UserInfo;
}

/** 注册请求参数 */
export interface RegisterParams {
  username: string;
  password: string;
  email?: string;
  phone?: string;
  avatar?: string;
}

/** 注册响应（后端返回 {message, user}，无 code 字段，前端兼容处理） */
export interface RegisterResponse {
  message: string;
  user: UserInfo | null;
}

/** 重置密码请求参数 */
export interface ResetPasswordParams {
  username: string;
  email: string;
  phone: string;
  new_password: string;
}

/** 重置密码响应 */
export interface ResetPasswordResponse {
  code: number;
  message: string;
}

/** verify 响应（带 code/message/data 标准信封） */
export interface VerifyResponse {
  code: number;
  message: string;
  data: UserInfo | null;
}

/** 修改密码请求参数 */
export interface ChangePasswordParams {
  old_password: string;
  new_password: string;
}

/** 修改密码响应 */
export interface ChangePasswordResponse {
  code: number;
  message: string;
}

/**
 * 更新用户信息请求参数。
 * 注意：后端此接口使用 multipart/form-data（username/email/phone 为 form 字段 + 可选 avatar 文件）。
 * 调用方需自行构造 multipart 请求（@ohos.request.agent 或手写 multipart body）。
 */
export interface UpdateUserInfoParams {
  username: string;
  email: string;
  phone: string;
}
