import { Request } from '../../net/Request';
import { ApiRoutes } from '../../constants/ApiRoutes';
import { ApiResponse } from '../../net/Response';
import type { UserConnectionsData, OnlineUsersData, WsStats } from './WsTypes';

/**
 * WsApi
 * WebSocket 管理域 REST 调用，对标 Android api/ws/WsApi.kt。
 */
export class WsApi {
  static async getMyDevices(): Promise<ApiResponse<UserConnectionsData>> {
    return await Request.get<ApiResponse<UserConnectionsData>>(ApiRoutes.WS_MY_DEVICES);
  }

  static async getOnlineUsers(): Promise<ApiResponse<OnlineUsersData>> {
    return await Request.get<ApiResponse<OnlineUsersData>>(ApiRoutes.WS_ONLINE);
  }

  static async getStats(): Promise<ApiResponse<WsStats>> {
    return await Request.get<ApiResponse<WsStats>>(ApiRoutes.WS_STATS);
  }
}
