package vpn

import "sync"

// 托管身份私钥只放在服务进程内存里：随进程启动建立、随进程退出消失，
// 不写数据库、不进 Redis，因此也不需要加密主密钥配置。
// 服务重启后内存为空，设备下次取身份时自动重新生成并下发——客户端不持久化私钥，
// 所以这只是一次“重新签发”，不会长期不可用。
var hostedKeys = struct {
	sync.RWMutex
	keys map[uint]string // VPNDevice.ID -> WireGuard 私钥（base64）
}{keys: make(map[uint]string)}

func storeHostedKey(deviceRowID uint, privateKey string) {
	hostedKeys.Lock()
	defer hostedKeys.Unlock()
	hostedKeys.keys[deviceRowID] = privateKey
}

func loadHostedKey(deviceRowID uint) (string, bool) {
	hostedKeys.RLock()
	defer hostedKeys.RUnlock()
	key, ok := hostedKeys.keys[deviceRowID]
	return key, ok
}

func forgetHostedKey(deviceRowID uint) {
	hostedKeys.Lock()
	defer hostedKeys.Unlock()
	delete(hostedKeys.keys, deviceRowID)
}

// clearHostedKeys 仅供测试使用，避免用例之间互相影响。
func clearHostedKeys() {
	hostedKeys.Lock()
	defer hostedKeys.Unlock()
	hostedKeys.keys = make(map[uint]string)
}
