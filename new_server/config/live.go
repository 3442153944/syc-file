package config

import (
	"sync/atomic"

	"syc-file/internal/supervisor"
)

// live 当前生效的配置快照。
//
// 读路径（HTTP handler、后台采集 goroutine 等）一律通过 Live() 拿一份快照；
// 写路径只在“热重载成功”后 PublishLive 一次。用“发布新副本”代替“原地改字段”，
// 读侧就永远不需要加锁，也不会读到改到一半的结构（slice 也不会被并发 append）。
var live atomic.Pointer[Config]

// Live 返回当前生效的配置快照。
//
// 返回值只读：调用方不得修改其中任何字段，也不能对里面的 slice 做 append/改元素。
// 需要修改时先 Clone() 一份，改完再 PublishLive()。
//
// 尚未 PublishLive 过时返回启动期加载的 Conf —— 保证“进程启动后 Live() == Conf”，
// 也让还没接入热重载的测试 / 工具代码照常工作。
func Live() *Config {
	if c := live.Load(); c != nil {
		return c
	}
	return Conf
}

// PublishLive 原子发布一份新的配置快照。
//
// c 必须是“发布之后不再被修改”的独立副本（通常是 Clone 出来的），否则读侧会看到并发写。
// 传 nil 会被忽略（避免 Live() 变成 nil 让调用方 panic，也避免误清空当前配置）。
func PublishLive(c *Config) {
	if c == nil {
		return
	}
	live.Store(c)
}

// Clone 深拷贝配置：slice 逐层复制，返回的副本与 c 不共享任何底层数组。
//
// 新增带引用语义的字段（slice / map / 指针）时必须同步补到这里，
// 否则“克隆 → 改热字段 → 发布”之后，新快照仍会和旧快照共享内存。
func Clone(c *Config) *Config {
	if c == nil {
		return nil
	}
	out := *c
	out.Whitelist = cloneStrings(c.Whitelist)
	out.Vpn.ServerLANEndpoints = cloneStrings(c.Vpn.ServerLANEndpoints)
	out.File.AllowedPaths = cloneStrings(c.File.AllowedPaths)
	out.File.Upload.AllowedExtensions = cloneStrings(c.File.Upload.AllowedExtensions)
	out.File.Upload.ForbiddenExtensions = cloneStrings(c.File.Upload.ForbiddenExtensions)
	out.User.AllowedExtensions = cloneStrings(c.User.AllowedExtensions)
	out.Supervisor.Processes = cloneProcessConfigs(c.Supervisor.Processes)
	return &out
}

// cloneStrings 复制字符串切片；nil 保持 nil，避免把 nil 和空切片混为一谈。
func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// cloneProcessConfigs 深拷贝 supervisor 的进程列表（Args 也是 slice，要单独复制）。
func cloneProcessConfigs(in []supervisor.ProcessConfig) []supervisor.ProcessConfig {
	if in == nil {
		return nil
	}
	out := make([]supervisor.ProcessConfig, len(in))
	for i, p := range in {
		out[i] = p
		out[i].Args = cloneStrings(p.Args)
	}
	return out
}
