# NAT 自动组网验收与端口预测边界

## 已实现并实测：探测期间保留中继

`nat-acceptance.py` 使用 `sys_core/examples/mesh_plan.rs` JSON-lines CLI；CLI 直接调用 `sys_core::mesh::plan`，没有第二套路由决策。真实 WireGuard 接口运行在现有 `filesync-mesh-nat-*` Docker 网络命名空间内。脚本只操作这些测试容器，不操作 VM 宿主防火墙、syc0 或生产服务。

修复前真实复现：服务端映射已建立，两端均开始探测未经确认的公网端点时，旧 planner 将远端 /32 交给 direct peer，ping 失败。中继虽然在线，却没有该 /32 的所有权。

修复后：

- 有服务器中继时，未经确认的 direct peer 不持有目标 AllowedIPs；WireGuard keepalive/握手继续探测。中继持续持有目标 /32。
- 有新鲜握手确认后，才将目标 /32 移交 direct peer。
- 不再周期性删除探测 peer；候选列表持续轮换，避免重建 peer 打断握手。
- 没有中继时仍给 direct peer 配置路由，不因探测窗口周期性移除唯一通路。
- Android/Windows 宿主配置允许空 AllowedIPs 的探测 peer。Android 不把 wg-quick 配置更新中的临时 DOWN 回调发布成用户断开。

2026-10-25 最终测试，实际15秒 planner cadence、两端不同 round phase：

| 阶段 | 结果 | 数据面证据 |
|---|---|---|
| 对称 NAT，服务端与对端目的地址对应不同映射 | round 0..5 全通过 | relay/relay，ping TTL63 |
| 改为独立端口映射，保留 conntrack 过滤 | round6 中继，round7自动直连 | relay TTL63 → direct/direct TTL64 |
| 再次改为对称 NAT，并改变外部映射端口 | round16..19 全通过 | 自动 relay/relay，TTL63 |

最终12次 probe 全成功，`failures=[]`。本测试从现有候选观测驱动实际 planner，**不再手工指定目标 /32 走中继**。网络重绑定阶段模拟协调服务已经更新端点；不能据此声称“候选保持旧值时立即识别直连失效”，当前握手 freshness 仍为180秒。

注意模型：仅 SNAT 固定外部端口不能完整模拟 endpoint-independent inbound mapping。可打洞阶段在测试路由器上显式建立入站 DNAT 映射，FORWARD 仍使用 conntrack 过滤。新 conntrack zone 隔离每轮映射，避免旧条目掩盖拓扑变化。TTL63/64分别证明通过中继/直接传输，不只检查 planner 的 mode 字符串。

## 运行方式

VM 需已有 Docker、wg、ip、iptables、nsenter、python3。容器由 `symmetric-nat.sh up` 创建；不要对已有运行容器重复 up，不要 Docker prune。

```sh
# 在包含最新 sys_core 源码的隔离副本中
cargo build --example mesh_plan --offline
python3 tests/mesh/nat-acceptance.py target/debug/examples/mesh_plan
```

Windows 工作区与 VM 缓存的依赖版本可能不同；本次只在 `/tmp/filesync-nat-acceptance/sys_core` 副本删除 lock 并离线解析，未改工作区 Cargo.lock。保留实际 `src/mesh.rs` 内容，不能另写简化 planner 替代被测实现。

脚本 finally 删除自有 natwg 接口并恢复两台测试路由器原有的40010/40011映射。测试容器仍保留。异常 SIGKILL 不会运行 finally，接手先检查自有接口和规则；正常退出已确认清理。

证据（工作区 ignored target）：

- `sys_core/target/mesh-test/nat-baseline.jsonl`：旧逻辑探测时断流复现。
- `sys_core/target/mesh-test/nat-final.jsonl`：最终成功的自动切换与实际TTL。
- `sys_core/target/mesh-test/nat-android-lan.txt`、`nat-android-cloud.txt`：新版APK、Clash VPN前/中/后不变，root独立接口与真实HTTP成功。
- `sys_core/target/mesh-test/nat-android-windows.txt`：Android访问Windows独立测试接口HTTP成功。

`run-android-acceptance.ps1 -TestPassword <运行时提供> [-Cloud] [-WindowsPeer]` 自动同步授权测试机公钥到自有测试网关。测试账号认证绑定 Device-Id，login 与后续请求必须一致；否则 devices/my 返回401。密码不写文件；脚本也会在结果写盘前防御性脱敏。WindowsPeer 需要先运行 `prepare-windows-peer.ps1`，并在独立172.1.0.4接口启动已有只读HTTP fixture；只创建 fs-mesh-test，不能把原syc0当测试接口。

## Android + Windows 共用一个 NAT，端口递增但步长不确定

用户提出的端口预测/并发打洞是可研究的增强项，**当前未实现生产端的并发端口预测传输，也未完成该场景的预测成功率实测**。上面通过的Linux隔离测试不冒充Android/Windows在随机端口分配器下的并发预测验收。

同一 NAT 下两台设备首先应交换并探测真实 LAN 候选；公网IP相同不代表LAN可达（Wi-Fi隔离、不同子网、CGNAT等），LAN失败时也不能假设NAT支持hairpin。

RFC5128 §3.5 描述了UDP端口预测，并明确指出：同一NAT后的另一台设备新建连接，会占用预测中的端口；随机分配、端口已占用、多层NAT等也可能使预测失效。因此递增模式只是短期概率线索，不是“固定步长”保证。[RFC5128](https://www.rfc-editor.org/rfc/rfc5128.html#section-3.5)

后续实现应满足：

1. **采样与真实传输一致。** 独立STUN socket的映射不等于WG socket的映射。Android kernel/GoBackend与Windows原生WG目前持有UDP socket，不能另开本地socket探测后套用结果。可研究用同一WG接口对多个协调观察节点建立只握手、不承载网段的peer，让观察节点回报同一个WG发送端口的映射；分类address-dependent mapping还需要不同观察IP，不能只换同一IP上的端口。
2. **样本隔离。** 按设备公钥、连接代际、网络代际、源socket、观察目标存样本；Android与Windows样本不能混为同一个socket。公网IP相同只能作为可能共享NAT的线索，不能直接合并身份。换Wi-Fi/蜂窝、重连、端口回绕、IP池变化使旧模型失效。
3. **推断候选集合，而非一个端口。** 正增量的众数/GCD可作为线索，但另一设备的交错连接可能使观测增量成为基础步长的倍数；GCD=1不表示下一端口必然+1。需要同时考虑临近窗口、多个可能步长和采样后的分配干扰，置信度不足就停止预测。
4. **并发受资源预算限制。** 可先评估最多32个候选、8个并发、约10秒单轮预算；这是待实测的设计值，未作为生产默认值落地。两设备共享NAT时还需要协调总预算，避免每个探测目标都产生新映射，反过来扰乱分配规律。
5. **WireGuard endpoint不是并发列表。** 当前每peer只有一个endpoint，频繁wg set只是串行轮换，还可能重置握手。真正并发需要跨Android/Windows一致的发送/接收机制，且握手包的源端点必须与测得的映射一致；不能用普通UDP旁路探测的成功代替WG成功。
6. **以认证握手和实际数据验证。** 预测包有响应不等于直连成功。使用对应公钥的新鲜WG握手、实际endpoint、传输与TTL/HTTP验证。预测期间保持现有中继；候选失败只结束本轮探测。

建议补充的隔离验收矩阵（尚未执行）：

| 场景 | 应验证的行为 |
|---|---|
| Android与Windows在同一NAT、LAN可达 | 优先LAN，不必端口预测 |
| 同NAT、LAN被隔离、支持/不支持hairpin | 分别验证公网回环或中继，不误判 |
| 两端跨NAT，一侧NAT还有第二设备 | 测另一设备交错分配引起的步长跳变 |
| 固定步长1/2/3，再引入不固定连接间隔 | 候选窗口命中率，保持中继 |
| 步长变化、随机端口、端口池耗尽、多层NAT | 置信度降低并及时终止预测 |
| 测量与打洞期间切网 | 丢弃旧代际样本，不尝试旧映射 |

## CLI 外部端点上报修正

此前 `nat --report --wg-port 51821` 用STUN的IP拼接本地监听端口，这仍然错误：两设备都监听51821，NAT可分配不同外部端口，甚至普通端点无关映射也不保证端口保持。

现改为：`--wg-port`仅展示本地信息；上报必须显式提供 `--observed-wg-endpoint`（实际WG流量的外部观察值），校验后保留完整ip:port，不自动改端口。新增/调整回归覆盖共享公网IP不同外部端口、IPv6及非法值。独立STUN结果仍只用于诊断。
