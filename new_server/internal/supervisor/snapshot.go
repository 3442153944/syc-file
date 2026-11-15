package supervisor

import "syc-file/internal/desktopapi"

// Snapshot 返回被托管进程的当前状态快照，供桌面控制台概览页展示。
//
// 单独成文件而不是改 supervisor.go：后者可能正被其它改动占用，
// 这里只读取已有的 cfg/running，不新增状态、不改变托管行为。
// 拿不到的字段（重启次数、最近错误）保持零值，GUI 会显示为「—」。
func (s *Supervisor) Snapshot() []desktopapi.ProcessStatus {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]desktopapi.ProcessStatus, 0, len(s.cfg.Processes))
	for _, pc := range s.cfg.Processes {
		if !pc.Enabled {
			continue
		}
		ps := desktopapi.ProcessStatus{Name: pc.Name}
		if cmd, ok := s.running[pc.Name]; ok && cmd != nil && cmd.Process != nil {
			ps.Running = true
			ps.PID = cmd.Process.Pid
		}
		out = append(out, ps)
	}
	return out
}
