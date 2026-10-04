// Package system 服务器初始化、权限级别、路由下发与访客账号。
//
//   - 初始化：服务器首次启动没有任何超级管理员，此时 /v1/system/status 报告 initialized=false，
//     客户端据此进入初始化向导；向导凭「一次性初始化码」创建第一个超级管理员（见 init.go）。
//   - 权限级别：游客(0) < 用户(1) < 管理员(2) < 超级管理员(3)，写在 user.level。
//   - 路由下发：客户端只内置基础路由，其余由 GET /v1/routes 按调用者级别返回（见 routes.go）。
//   - 访客：管理员下发的临时账号，只能访问被指定的页面及其对应接口（见 guests.go / guest_perm.go）。
package system

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

const settingInitialized = "initialized"

var (
	initMu      sync.Mutex // 保护 setupCode / 失败计数 / 初始化动作本身
	initialized atomic.Bool
	setupCode   string
	failCount   int
	lockUntil   time.Time
)

// Initialized 服务器是否已完成初始化。
func Initialized() bool { return initialized.Load() }

// Bootstrap 启动时调用（AutoMigrate 之后）：升级旧数据、播种路由、判定初始化状态。全部幂等。
func Bootstrap(db *gorm.DB) error {
	if err := migrateLegacyLevels(db); err != nil {
		return err
	}
	if err := seedRoutes(db); err != nil {
		return err
	}

	if v, ok := getSetting(db, settingInitialized); ok && v == "true" {
		initialized.Store(true)
		return nil
	}
	// 老部署：已经有（升级而来的）超级管理员，视为已初始化，不能把线上系统拉进向导
	var supers int64
	if err := db.Model(&model.User{}).Where("level = ?", model.LevelSuper).Count(&supers).Error; err != nil {
		return fmt.Errorf("统计超级管理员失败: %w", err)
	}
	if supers > 0 {
		if err := setSetting(db, settingInitialized, "true"); err != nil {
			return err
		}
		initialized.Store(true)
		return nil
	}

	code := normalizeCode(os.Getenv("SYC_SETUP_CODE"))
	if code == "" {
		code = randomCode()
	}
	initMu.Lock()
	setupCode = code
	initMu.Unlock()
	announceSetupCode(code)
	return nil
}

// migrateLegacyLevels 把升级前的权限数据收敛成新模型，幂等。
//
// 超级管理员全系统只有一个：
//  1. 还没有超管时，升级前的管理员（role=admin 但 level 仍是默认 1）里**最早创建的那位**成为超管，
//     并保持原来「注册开放」的行为；
//  2. 其余旧管理员成为普通管理员（level 2）；
//  3. 兜底：若库里已有多个超管（不应出现），保留 id 最小的，其余降为管理员。
//
// 靠 role 与 level 始终同步（见 model.RoleOfLevel）来避免把后来被降级的管理员又升回去。
func migrateLegacyLevels(db *gorm.DB) error {
	var supers int64
	if err := db.Model(&model.User{}).Where("level = ?", model.LevelSuper).Count(&supers).Error; err != nil {
		return fmt.Errorf("统计超级管理员失败: %w", err)
	}
	if supers == 0 {
		var first model.User
		err := db.Where("role = ? AND level < ?", "admin", model.LevelAdmin).Order("id asc").First(&first).Error
		switch {
		case err == nil:
			if err := db.Model(&model.User{}).Where("id = ?", first.ID).
				Updates(map[string]interface{}{"level": model.LevelSuper, "allow_register": true}).Error; err != nil {
				return fmt.Errorf("升级旧管理员为超级管理员失败: %w", err)
			}
			logger.Logger.Info("已将最早的旧管理员升级为超级管理员",
				zap.Uint("user_id", first.ID), zap.String("username", first.Username))
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("查找旧管理员失败: %w", err)
		}
	}

	res := db.Model(&model.User{}).
		Where("role = ? AND level < ?", "admin", model.LevelAdmin).
		Update("level", model.LevelAdmin)
	if res.Error != nil {
		return fmt.Errorf("升级其余旧管理员失败: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		logger.Logger.Info("其余旧管理员已设为管理员", zap.Int64("count", res.RowsAffected))
	}

	var superIDs []uint
	if err := db.Model(&model.User{}).Where("level = ?", model.LevelSuper).Order("id asc").Pluck("id", &superIDs).Error; err != nil {
		return fmt.Errorf("读取超级管理员失败: %w", err)
	}
	if len(superIDs) > 1 {
		if err := db.Model(&model.User{}).Where("id IN ?", superIDs[1:]).
			Updates(map[string]interface{}{"level": model.LevelAdmin, "allow_register": false}).Error; err != nil {
			return fmt.Errorf("收敛多余的超级管理员失败: %w", err)
		}
		logger.Logger.Warn("超级管理员只能有一个，多余的已降为管理员", zap.Uints("demoted", superIDs[1:]))
	}
	return nil
}

func announceSetupCode(code string) {
	banner := fmt.Sprintf(`
==============================================================
  服务器尚未初始化
  请在桌面端「初始化向导」中输入下面的一次性初始化码：

      %s

  （可用环境变量 SYC_SETUP_CODE 预先指定；初始化完成后失效）
==============================================================
`, code)
	fmt.Fprint(os.Stderr, banner)
	logger.Logger.Warn("服务器尚未初始化，等待初始化向导", zap.String("setup_code", code))
}

// ── 初始化码 ─────────────────────────────────────────────────

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去掉易混的 I O 0 1

func randomCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // 系统随机源不可用，无法安全生成，直接让进程起不来
	}
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out[:4]) + "-" + string(out[4:])
}

// normalizeCode 大写并去掉空格 / 连字符，方便用户手输。
func normalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	return strings.ReplaceAll(s, " ", "")
}

// ── app_setting 读写 ─────────────────────────────────────────

func getSetting(db *gorm.DB, name string) (string, bool) {
	var row model.AppSetting
	if err := db.Where("name = ?", name).First(&row).Error; err != nil {
		return "", false
	}
	return row.Value, true
}

func setSetting(db *gorm.DB, name, value string) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&model.AppSetting{Name: name, Value: value}).Error
}
