package vpn

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
)

// StartServerReconciler 让后端自己维护服务器本机 WireGuard 接口的 peer 列表。
//
// 规则：
//   - 只有 status=active 且 session_expires_at > now 的设备才会被登记为 peer；
//   - session_expires_at 由登录/注册写入、heartbeat 每 30s 续期，
//     因此「用户会话过期」才是 peer 被移除的时机，而不是某个外部脚本的 token 过期；
//   - 每 30s 对账一次：缺的加上，多的删掉。
//
// 配置 vpn.server_interface（例如 syc0）才启用；需要 root 权限操作 wg。
func StartServerReconciler(db *gorm.DB) {
	iface := strings.TrimSpace(config.Conf.Vpn.ServerInterface)
	if iface == "" {
		return
	}
	if os.Geteuid() != 0 {
		logger.Logger.Warn("组网 peer 同步需要 root，当前不是 root，已跳过", zap.String("iface", iface))
		return
	}
	wgBin, err := ensureWgBinary()
	if err != nil {
		logger.Logger.Warn("组网 peer 同步未启动：找不到可用的 wg", zap.Error(err))
		return
	}
	logger.Logger.Info("组网 peer 同步已启动", zap.String("iface", iface), zap.String("wg", wgBin))

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			reconcilePeers(db, iface, wgBin)
			<-ticker.C
		}
	}()
}

func reconcilePeers(db *gorm.DB, iface, wgBin string) {
	// 临时停用到期自动恢复
	now := time.Now()
	db.Model(&model.VPNDevice{}).
		Where("status = ? AND suspended_until IS NOT NULL AND suspended_until <= ?", model.VPNDeviceSuspended, now).
		Updates(map[string]any{"status": model.VPNDeviceActive, "suspended_until": nil})

	var devices []model.VPNDevice
	err := db.Where("status = ? AND session_expires_at IS NOT NULL AND session_expires_at > ?",
		model.VPNDeviceActive, now).Find(&devices).Error
	if err != nil {
		logger.Logger.Warn("组网 peer 对账：读取设备失败", zap.Error(err))
		return
	}
	desired := make(map[string]string, len(devices))
	for _, d := range devices {
		if strings.TrimSpace(d.PubKey) == "" || strings.TrimSpace(d.Address) == "" {
			continue
		}
		desired[d.PubKey] = d.Address + "/32"
	}

	out, err := exec.Command(wgBin, "show", iface, "peers").Output()
	if err != nil {
		return // 接口不存在时静默（部署前接口还没建）
	}
	for _, pub := range strings.Fields(string(out)) {
		if _, ok := desired[pub]; ok {
			continue
		}
		// 会话过期 / 设备被吊销：从服务器接口移除
		_ = exec.Command(wgBin, "set", iface, "peer", pub, "remove").Run()
		logger.Logger.Info("组网 peer 已移除（会话过期或吊销）", zap.String("pubkey", pub[:8]))
	}
	for pub, ips := range desired {
		_ = exec.Command(wgBin, "set", iface, "peer", pub, "allowed-ips", ips).Run()
	}
}

// ensureWgBinary 复制一份 wg 再用：部分机器上 AppArmor 会拦 /usr/bin/wg 打开普通文件
// （fopen: Permission denied），复制出来的副本不受路径规则限制。
func ensureWgBinary() (string, error) {
	src, err := exec.LookPath("wg")
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dst := "/tmp/syc-wg-backend"
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return "", err
	}
	return dst, nil
}
