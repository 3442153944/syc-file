package device_store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"time"
)

const (
	deviceKeyPrefix = "device:online:" // device:online:{deviceId}
	userDevicesKey  = "user:devices:"  // user:devices:{userId}  存该用户所有在线设备ID
	deviceTTL       = 10 * time.Second // 心跳10s一次，30s没刷新视为离线
)

type DriverDetailInfo struct {
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name"`
	Manufacturer  string `json:"manufacturer"`
	Model         string `json:"model"`
	OsVersion     string `json:"os_version"`
	NetworkType   string `json:"network_type"`
	WifiSsid      string `json:"wifi_ssid"`
	IpAddress     string `json:"ip_address"`
	BatteryLevel  int    `json:"battery_level"`
	IsCharging    bool   `json:"is_charging"`
	AppVersion    string `json:"app_version"`
	ConnID        string `json:"conn_id"` // 关联的 ws 连接ID
	UserID        uint   `json:"user_id"`
	OnlineAt      int64  `json:"online_at"`
	LastHeartbeat int64  `json:"last_heartbeat"`
}

type DeviceStore struct {
	rdb *redis.Client
}

var Global *DeviceStore

func Init(rdb *redis.Client) {
	Global = &DeviceStore{rdb: rdb}
}

func New(rdb *redis.Client) *DeviceStore {
	return &DeviceStore{rdb: rdb}
}

// 设备上线
func (s *DeviceStore) Online(ctx context.Context, info DriverDetailInfo) error {
	info.OnlineAt = time.Now().Unix()
	info.LastHeartbeat = time.Now().Unix()

	data, err := json.Marshal(info)
	if err != nil {
		return err
	}

	pipe := s.rdb.Pipeline()
	// 存设备详情
	pipe.Set(ctx, deviceKey(info.DeviceID), data, deviceTTL)
	// 把设备ID加入用户的设备集合
	pipe.SAdd(ctx, userKey(info.UserID), info.DeviceID)
	_, err = pipe.Exec(ctx)
	return err
}

// 心跳刷新
func (s *DeviceStore) Heartbeat(ctx context.Context, deviceID string, userID uint) error {
	key := deviceKey(deviceID)

	// 先取出来更新 LastHeartbeat
	data, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	var info DriverDetailInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	info.LastHeartbeat = time.Now().Unix()

	updated, _ := json.Marshal(info)
	return s.rdb.Set(ctx, key, updated, deviceTTL).Err() // 刷新过期时间
}

// 设备下线
func (s *DeviceStore) Offline(ctx context.Context, deviceID string, userID uint) error {
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, deviceKey(deviceID))
	pipe.SRem(ctx, userKey(userID), deviceID)
	_, err := pipe.Exec(ctx)
	return err
}

// 获取用户所有在线设备
func (s *DeviceStore) GetUserDevices(ctx context.Context, userID uint) ([]DriverDetailInfo, error) {
	deviceIDs, err := s.rdb.SMembers(ctx, userKey(userID)).Result()
	if err != nil {
		return nil, err
	}

	var devices []DriverDetailInfo
	for _, id := range deviceIDs {
		data, err := s.rdb.Get(ctx, deviceKey(id)).Bytes()
		if err != nil {
			// key 已过期但 set 里还有，清理掉
			s.rdb.SRem(ctx, userKey(userID), id)
			continue
		}
		var info DriverDetailInfo
		if json.Unmarshal(data, &info) == nil {
			devices = append(devices, info)
		}
	}
	return devices, nil
}

// IsOnline 判断设备是否在线
func (s *DeviceStore) IsOnline(ctx context.Context, deviceID string) bool {
	return s.rdb.Exists(ctx, deviceKey(deviceID)).Val() > 0
}

func deviceKey(deviceID string) string {
	return fmt.Sprintf("%s%s", deviceKeyPrefix, deviceID)
}

func userKey(userID uint) string {
	return fmt.Sprintf("%s%d", userDevicesKey, userID)
}
