package model

import "time"

// Device 设备表
type Device struct {
	ID         uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint       `gorm:"not null;index" json:"user_id"`
	DeviceName string     `gorm:"size:100;not null" json:"device_name"`
	DeviceType string     `gorm:"size:20;not null;index" json:"device_type"`
	DeviceID   string     `gorm:"size:100;not null" json:"device_id"`
	OSVersion  string     `gorm:"size:50" json:"os_version"`
	AppVersion string     `gorm:"size:50" json:"app_version"`
	IPAddress  string     `gorm:"size:50" json:"ip_address"`
	LastActive *time.Time `json:"last_active"`
	Status     int8       `gorm:"not null;default:1;index" json:"status"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Device) TableName() string { return "device" }

const (
	DeviceTypeMobile  = "mobile"
	DeviceTypeWeb     = "web"
	DeviceTypeWindows = "windows"
	DeviceTypeMac     = "mac"
	DeviceTypeLinux   = "linux"
)

const (
	DeviceStatusActive   = 1
	DeviceStatusInactive = 0
)
