package model

import "time"

// OperationLog 操作日志表
type OperationLog struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID          *uint     `gorm:"index" json:"user_id"`
	DeviceID        *uint     `json:"device_id"`
	OperationType   *string   `gorm:"size:50;index" json:"operation_type"`
	OperationModule *string   `gorm:"size:50" json:"operation_module"`
	OperationDesc   *string   `gorm:"type:text" json:"operation_desc"`
	RequestMethod   *string   `gorm:"size:10" json:"request_method"`
	RequestURL      *string   `gorm:"size:500" json:"request_url"`
	RequestParams   *string   `gorm:"type:text" json:"request_params"`
	ResponseResult  *string   `gorm:"type:text" json:"response_result"`
	IPAddress       *string   `gorm:"size:50" json:"ip_address"`
	UserAgent       *string   `gorm:"size:500" json:"user_agent"`
	Status          *int8     `json:"status"`
	ErrorMessage    *string   `gorm:"type:text" json:"error_message"`
	ExecutionTime   *int      `json:"execution_time"`
	CreatedAt       time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}

func (OperationLog) TableName() string { return "operation_log" }
