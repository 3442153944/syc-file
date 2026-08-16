package ws

import (
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// 设备登记：把 WS 连上来的设备落到 device 表。
//
// 在此之前 device 表是空的——在线设备只存在于 Hub 的内存里，进程一重启就什么都不剩，
// 于是「设备列表 / 最后在线时间 / 离线设备」这类管理功能全都无从做起。
// 现在：连上来 upsert 一行并置在线，断开时置离线并记最后活跃时间。
//
// 设备身份以客户端自报的 device_id 为准（三端都持久化了一个稳定 id），
// 同一 device_id 换了用户就把归属改过去（同一台机器换账号登录）。

var deviceDB *gorm.DB

// initDeviceRegistry 由 InitWS 注入 db 并挂上连接钩子。
func initDeviceRegistry(db *gorm.DB) {
	deviceDB = db
}

// touchDeviceOnline 连接建立时登记/更新设备行。
func touchDeviceOnline(conn *Connection) {
	if deviceDB == nil || conn == nil || conn.Device == nil || conn.Device.DeviceID == "" {
		return
	}
	now := time.Now()
	d := conn.Device

	var existing model.Device
	err := deviceDB.Where("device_id = ?", d.DeviceID).First(&existing).Error
	if err == nil {
		updates := map[string]interface{}{
			"user_id":     conn.UserID,
			"device_type": string(d.DeviceType),
			"os_version":  d.Platform,
			"app_version": d.AppVersion,
			"ip_address":  conn.IP,
			"last_active": now,
			"status":      model.DeviceStatusActive,
		}
		// 设备名允许管理员在后台改，客户端上报的空值不要把它冲掉
		if d.DeviceName != "" && existing.DeviceName == "" {
			updates["device_name"] = d.DeviceName
		}
		if err := deviceDB.Model(&existing).Updates(updates).Error; err != nil {
			logger.Logger.Warn("更新设备登记失败", zap.String("device_id", d.DeviceID), zap.Error(err))
		}
		return
	}

	name := d.DeviceName
	if name == "" {
		name = string(d.DeviceType)
	}
	row := model.Device{
		UserID:     conn.UserID,
		DeviceName: name,
		DeviceType: string(d.DeviceType),
		DeviceID:   d.DeviceID,
		OSVersion:  d.Platform,
		AppVersion: d.AppVersion,
		IPAddress:  conn.IP,
		LastActive: &now,
		Status:     model.DeviceStatusActive,
	}
	if err := deviceDB.Create(&row).Error; err != nil {
		logger.Logger.Warn("登记设备失败", zap.String("device_id", d.DeviceID), zap.Error(err))
		return
	}
	logger.Logger.Info("新设备已登记", zap.String("device_id", d.DeviceID),
		zap.String("name", name), zap.Uint("user_id", conn.UserID))
}

// markDeviceOffline 连接断开时置离线。
// 同一设备可能有多条连接（少见但可能），还有别的连接在就不置离线。
func markDeviceOffline(conn *Connection) {
	if deviceDB == nil || conn == nil || conn.Device == nil || conn.Device.DeviceID == "" {
		return
	}
	if GetHub().IsDeviceOnline(conn.Device.DeviceID) {
		return
	}
	now := time.Now()
	if err := deviceDB.Model(&model.Device{}).Where("device_id = ?", conn.Device.DeviceID).
		Updates(map[string]interface{}{"status": model.DeviceStatusInactive, "last_active": now}).Error; err != nil {
		logger.Logger.Warn("标记设备离线失败", zap.String("device_id", conn.Device.DeviceID), zap.Error(err))
	}
}
