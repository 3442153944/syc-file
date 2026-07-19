import { Request } from '../../net/Request';
import { ApiRoutes } from '../../constants/ApiRoutes';
import { ApiResponse } from '../../net/Response';
import { TokenManager } from '../../token/tokenManager';
import type {
  LoginParams,
  LoginData,
  RegisterParams,
  RegisterResponse,
  ResetPasswordParams,
  ResetPasswordResponse,
  VerifyResponse,
  ChangePasswordParams,
  ChangePasswordResponse,
  UserInfo,
} from './UserTypes';

/**
 * UserApi
 * 职责：用户相关 API 调用封装，每个函数只组装参数 + 调 Request，不含业务逻辑。
 * 登录成功后由本类负责把 token 写入 TokenManager（对标 Android request.kt TOKEN_ENDPOINTS 自动提取逻辑）。
 */
export class UserApi {
  /** 用户登录。成功后保存 token。返回 ApiResponse 信封（含 LoginData）。 */
  static async login(params: LoginParams): Promise<ApiResponse<LoginData>> {
    const resp = await Request.post<ApiResponse<LoginData>>(ApiRoutes.USER_LOGIN, params as Object);
    if (resp.data && resp.data.token && resp.data.token.length > 0) {
      await TokenManager.getInstance().setToken(resp.data.token);
    }
    return resp;
  }

  /** 用户注册。后端返回 {message, user} 无 code，但 Request 兼容 code===undefined 视为成功。 */
  static async register(params: RegisterParams): Promise<RegisterResponse> {
    return await Request.post<RegisterResponse>(ApiRoutes.USER_REGISTER, params as Object);
  }

  /** 重置密码 */
  static async resetPassword(params: ResetPasswordParams): Promise<ResetPasswordResponse> {
    return await Request.post<ResetPasswordResponse>(ApiRoutes.USER_RESET_PASSWORD, params as Object);
  }

  /**
   * 校验 Token 有效性，返回当前用户信息（信封 code/message/data 三段都可用）。
   */
  static async verify(): Promise<VerifyResponse> {
    return await Request.post<VerifyResponse>(ApiRoutes.USER_VERIFY);
  }

  /** 修改密码（需旧密码） */
  static async changePassword(params: ChangePasswordParams): Promise<ChangePasswordResponse> {
    return await Request.post<ChangePasswordResponse>(ApiRoutes.USER_CHANGE_PASSWORD, params as Object);
  }
}
