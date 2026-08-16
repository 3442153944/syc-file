package admin

import (
	"strings"

	"github.com/gin-gonic/gin"

	"syc-file/internal/model"
	"syc-file/internal/ws"
)

// deviceRow 设备列表行：库里的登记信息 + Hub 里的实时在线状态。
// 库里的 status 是「上次断开时写下的」，进程重启后可能是脏的，所以在线与否**以 Hub 为准**。
type deviceRow struct {
	model.Device
	Online bool `json:"online"`
	// 该设备当前积压的同步任务数（pending/syncing），排查「某台设备一直不同步」用
	PendingTasks int64 `json:"pending_tasks"`
}

// ListDevices GET /v1/admin/devices?user_id=&keyword=&online=&page=&page_size=
//
// 普通用户只能看自己的设备；管理员不带 user_id 时看全部。
func (h *APIHandler) ListDevices(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	isAdmin := hasAdminRole(c)

	p := parsePage(c)
	q := h.db.Model(&model.Device{})
	switch {
	case !isAdmin:
		q = q.Where("user_id = ?", uid) // 非管理员强制只看自己的
	case c.Query("user_id") != "":
		q = q.Where("user_id = ?", c.Query("user_id"))
	}
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("device_name LIKE ? OR device_id LIKE ? OR ip_address LIKE ?", like, like, like)
	}

	var total int64
	q.Count(&total)

	var devices []model.Device
	if err := q.Order("last_active desc").Offset(p.offset()).Limit(p.PageSize).Find(&devices).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	hub := ws.GetHub()
	onlyOnline := c.Query("online") == "1"
	rows := make([]deviceRow, 0, len(devices))
	for _, d := range devices {
		online := hub.IsDeviceOnline(d.DeviceID)
		if onlyOnline && !online {
			continue
		}
		var pending int64
		h.db.Model(&model.SyncTask{}).
			Where("target_device_id = ? AND sync_status IN ?", d.DeviceID,
				[]string{model.SyncStatusPending, model.SyncStatusSyncing}).
			Count(&pending)
		rows = append(rows, deviceRow{Device: d, Online: online, PendingTasks: pending})
	}
	jsonPage(c, rows, total, p)
}

// UpdateDevice PUT /v1/admin/devices/:id —— 重命名 / 启停用。
// 设备主人本人或管理员可改。
func (h *APIHandler) UpdateDevice(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		DeviceName *string `json:"device_name"`
		Status     *int8   `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	var d model.Device
	if err := h.db.First(&d, id).Error; err != nil {
		jsonErr(c, 404, "设备不存在")
		return
	}
	if d.UserID != uid && !hasAdminRole(c) {
		jsonErr(c, 403, "无权操作该设备")
		return
	}
	updates := map[string]interface{}{}
	if req.DeviceName != nil && strings.TrimSpace(*req.DeviceName) != "" {
		updates["device_name"] = strings.TrimSpace(*req.DeviceName)
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if len(updates) == 0 {
		jsonErr(c, 400, "没有需要更新的字段")
		return
	}
	if err := h.db.Model(&d).Updates(updates).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, nil)
}

// KickDevice POST /v1/admin/devices/:id/kick —— 把设备踢下线（不删登记）。
func (h *APIHandler) KickDevice(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var d model.Device
	if err := h.db.First(&d, id).Error; err != nil {
		jsonErr(c, 404, "设备不存在")
		return
	}
	if d.UserID != uid && !hasAdminRole(c) {
		jsonErr(c, 403, "无权操作该设备")
		return
	}
	if !ws.GetHub().DisconnectDevice(d.DeviceID) {
		jsonErr(c, 404, "设备当前不在线")
		return
	}
	jsonOK(c, nil)
}

// DeleteDevice DELETE /v1/admin/devices/:id —— 解绑设备。
//
// 先踢下线再删登记行；**不删它的同步任务**：任务是按 device_id 派的，
// 设备重新连上来（同一 device_id）还会继续执行，静默清掉等于让那台设备永远缺一批文件。
// 真要清任务走同步页的「清理记录」。
func (h *APIHandler) DeleteDevice(c *gin.Context) {
	uid, ok := currentUser(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var d model.Device
	if err := h.db.First(&d, id).Error; err != nil {
		jsonErr(c, 404, "设备不存在")
		return
	}
	if d.UserID != uid && !hasAdminRole(c) {
		jsonErr(c, 403, "无权操作该设备")
		return
	}
	ws.GetHub().DisconnectDevice(d.DeviceID)
	if err := h.db.Delete(&d).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, nil)
}
