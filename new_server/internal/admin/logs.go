package admin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"syc-file/internal/model"
)

// logRow 日志行 + 用户名（前端不用再为每行去查用户）。
type logRow struct {
	model.OperationLog
	Username string `json:"username"`
}

// ListLogs GET /v1/admin/logs?user_id=&module=&type=&status=&keyword=&start=&end=&page=&page_size=
// start/end 为 Unix 秒。
func (h *APIHandler) ListLogs(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	p := parsePage(c)
	q := h.db.Model(&model.OperationLog{})

	if v := c.Query("user_id"); v != "" {
		q = q.Where("user_id = ?", v)
	}
	if v := c.Query("module"); v != "" {
		q = q.Where("operation_module = ?", v)
	}
	if v := c.Query("type"); v != "" {
		q = q.Where("operation_type = ?", v)
	}
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("keyword"); v != "" {
		like := "%" + v + "%"
		q = q.Where("request_url LIKE ? OR operation_desc LIKE ?", like, like)
	}
	if v := c.Query("start"); v != "" {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
			q = q.Where("created_at >= ?", time.Unix(sec, 0))
		}
	}
	if v := c.Query("end"); v != "" {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
			q = q.Where("created_at <= ?", time.Unix(sec, 0))
		}
	}

	var total int64
	q.Count(&total)

	var logs []model.OperationLog
	if err := q.Order("id desc").Offset(p.offset()).Limit(p.PageSize).Find(&logs).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}

	// 批量取用户名，避免 N+1
	names := map[uint]string{}
	ids := make([]uint, 0, len(logs))
	for _, l := range logs {
		if l.UserID != nil {
			ids = append(ids, *l.UserID)
		}
	}
	if len(ids) > 0 {
		var users []model.User
		h.db.Select("id, username").Where("id IN ?", ids).Find(&users)
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}

	rows := make([]logRow, 0, len(logs))
	for _, l := range logs {
		r := logRow{OperationLog: l}
		if l.UserID != nil {
			r.Username = names[*l.UserID]
		}
		rows = append(rows, r)
	}
	jsonPage(c, rows, total, p)
}

// ClearLogs DELETE /v1/admin/logs?before=<unix秒>
// 不带 before 时拒绝——避免手滑清空整张表。
func (h *APIHandler) ClearLogs(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	v := c.Query("before")
	if v == "" {
		jsonErr(c, 400, "必须指定 before（Unix 秒），只清理该时间之前的日志")
		return
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		jsonErr(c, 400, "before 格式错误")
		return
	}
	res := h.db.Where("created_at < ?", time.Unix(sec, 0)).Delete(&model.OperationLog{})
	if res.Error != nil {
		jsonErr(c, 500, res.Error.Error())
		return
	}
	jsonOK(c, gin.H{"deleted": res.RowsAffected})
}

// LogModules GET /v1/admin/logs/modules —— 现有模块列表，供前端筛选下拉。
func (h *APIHandler) LogModules(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var modules []string
	h.db.Model(&model.OperationLog{}).
		Distinct().Where("operation_module IS NOT NULL AND operation_module <> ''").
		Order("operation_module asc").Pluck("operation_module", &modules)
	jsonOK(c, modules)
}
