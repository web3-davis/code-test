# BTC 跨链转账 Demo（Solidity + Go + BTC + MPC）

## 运行

```bash
cd test3
go run .
```

不需要真实区块链、节点、数据库或 MPC 集群。

## 测试

```bash
cd test3
go test -v ./...
```

`internal/listener/event_listener_test.go` 用 `MockBackend` 断言状态和 BTC 转账次数：

- `TestListenerScenarios`：正常请求 Completed；重复事件只转一次 BTC；主网地址 Failed；Reorg 移除的日志、非目标合约的日志都保持 Pending。
- `TestRetryAfterStartFailure`：`startBTCTransfer` 失败（BTC 未发出）后重新投递事件，可以继续完成转账。
- `TestContextCancel`：等待 EVM 确认时 ctx 取消会立即返回。

## 流程

```
用户 requestBTCTransfer ──► BTCTransferRequested 事件
        │
Go Listener：校验合约地址/ChainID → 解析事件 → requestId 去重 → 等待 EVM 确认 + Reorg 检查
        │
startBTCTransfer（Pending → Processing）
        │
BitcoinTransfer：校验地址/网络/Dust → 选 UTXO → 构建 P2WPKH 交易 → 签名前审核
               → BIP143 摘要 → MPC Sign → DER 验签 → Witness → 脚本引擎校验 → 广播
        │
等待 BTC 确认 → completeBTCTransfer（Processing → Completed，抛出 BTCTransferCompleted）
```

## 目录

| 文件 | 职责 |
|---|---|
| `contracts/BTCTransfer.sol` | 请求记录与状态机，AccessControl / Pausable / ReentrancyGuard |
| `internal/listener/event_listener.go` | 事件订阅（含真实 WebSocket 示例）、解析、去重、Reorg、编排流程 |
| `internal/bitcoin/transfer.go` | btcsuite 构建、签名、序列化 P2WPKH 交易 |
| `internal/mpc/signer.go` | `Signer` 接口 + `MockMPCSigner` |
| `internal/ethereum/notifier.go` | ABI 编码、签名并发送合约调用；内存 Mock 链 |

## 哪些是 Mock

- **MockMPCSigner**：进程内用固定种子派生的测试私钥做普通 ECDSA 签名。签名真实有效（可通过 btcd 脚本引擎验证），但**不是 MPC**：真实 MPC 下完整私钥从不存在，由多方执行分布式签名协议。
- **UTXO / 广播 / BTC 确认**：`fetchUTXOs`、`broadcast`、`WaitForConfirmation` 为 Mock，只打印 Raw Tx 与 TxID。
- **EVM 链**：`MockBackend` 在内存中模拟 `BTCTransfer.sol` 的状态机和权限检查；`Notifier` 依赖的 `Backend` 接口可直接替换为 `*ethclient.Client`。
- **去重**：内存 Map，只演示幂等，重启后丢失；生产需持久化任务状态。

## 关键安全点

- 同一 `requestId` 只发起一次 BTC 转账；合约中 Completed 无法再次完成或改为 Failed。
- 只有“确定未广播”的错误才调用 `failBTCTransfer`；广播结果未知（`ErrBroadcastUnknown`）保持 Processing，等待对账。
- 广播成功 ≠ 确认，达到确认数后才通知 Completed；通知失败只重试通知，不重发 BTC。
- 签名前校验收款地址、金额、手续费上限、找零地址；签名后用公钥验签并执行脚本校验。
- 事件不代表资金授权：真实系统还需校验用户余额、额度与资金归属，MPC 侧需独立的审批策略。
- Completed 依赖 Operator 的可信通知，合约无法独立验证 BTC 链状态。
