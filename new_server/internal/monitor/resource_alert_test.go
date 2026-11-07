package monitor

import (
	"testing"

	"syc-file/pkg/synccore"
)

// TestAlertCorroborated 校验“无进程归因的核心忙”会被判定为宿主争用噪声：
// sysinfo 的 cpu_percent 按逻辑核数归一化，乘回核数才是“相当于几个单核”。
func TestAlertCorroborated(t *testing.T) {
	mk := func(cpu float32) []synccore.ProcessInfo {
		return []synccore.ProcessInfo{{CPUPercent: cpu}}
	}
	cases := []struct {
		name    string
		procs   []synccore.ProcessInfo
		numCpus uint32
		want    bool
	}{
		{"8核下单个进程吃满半核", mk(6.3), 8, true}, // 6.3%*8=50.4%
		{"8核下只有零散进程", mk(4.6), 8, false},  // 36.8%
		{"无进程快照但有核数", nil, 8, false},      // 采集失败/无归因
		{"拿不到核数时保守放行", mk(1.0), 0, true},  // 宁可多报
		{"16核下1.2个核", mk(7.5), 16, true},  // 120%
		{"16核下都不足半核", []synccore.ProcessInfo{{CPUPercent: 2.0}, {CPUPercent: 1.0}}, 16, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alertCorroborated(tc.procs, tc.numCpus); got != tc.want {
				t.Fatalf("alertCorroborated(%v, %d) = %v, want %v", tc.procs, tc.numCpus, got, tc.want)
			}
		})
	}
}
