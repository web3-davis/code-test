# AI Coding 面试题汇总

本仓库包含 3 道 AI Coding 题目，每道题目录下都有：

- `project.md`：题目要求与开发方案
- `README.md`：实现说明、设计原理与安全边界

| 目录 | 题目 | 验证方式 |
| --- | --- | --- |
| [test1](test1/README.md) | 以太坊入金检测（ETH / ERC20 / 合约内部 ETH，防假入金） | 单元测试 |
| [test2](test2/README.md) | Solana SPL Token 交易（ATA、Message、签名、Raw Tx 解析） | 单元测试 + 运行 Demo |
| [test3](test3/README.md) | Solidity + Go + BTC + MPC 跨链转账 | 单元测试 + 运行 Demo |

## 环境要求

- Go 1.24+
- 三个题目均使用本地模拟数据，**不需要**真实节点、RPC、数据库或 MPC 集群
- 首次运行会自动下载 Go 依赖（需联网）

> 注意：`test1`、`test2` 共用仓库根目录的 `go.mod`（模块名 `risk`）；`test3` 有独立的 `go.mod`，需进入 `test3` 目录运行。

## test1：以太坊入金检测

在仓库根目录执行：

```bash
go test -v ./test1/
```

**预期**：`TestScanBlocks` 通过（`--- PASS`），日志打印 3 条入金记录，最后输出 `ok risk/test1`。测试同时断言每笔交易只请求一次 Receipt。

测试通过进程内模拟 RPC（`eth_chainId`、`eth_getBlockByNumber`、`eth_getTransactionReceipt`、`debug_traceTransaction`）构造一个区块，覆盖以下场景：

| 场景 | 预期结果 |
| --- | --- |
| 直接向充值地址转 ETH | 识别为 `ETH` 入金 |
| 白名单 USDT 合约的 `Transfer` 日志 | 识别为 `ERC20` 入金 |
| 合约内部转 ETH，其中一个子调用回滚 | 仅成功的子调用识别为 `INTERNAL_ETH` |
| Receipt 失败的交易 | 不入账（防假入金） |

重点代码：`test1/eth_deposit.go`、`test1/erc20_deposit.go`、`test1/internal_eth_deposit.go`。

## test2：Solana SPL Token 交易

在仓库根目录执行：

```bash
go test -v ./test2/   # 单元测试
go run ./test2        # 运行 Demo
```

**测试预期**：`TestRoundTrip`（构建 → 签名 → 序列化 → 解析，结果与构建时一致）和 `TestDecodeInvalid`（非法输入返回 error 而不是 panic）均 `PASS`。

**Demo 预期**：输出 4 段结果，依次对应：

1. `PrepareAccount`：计算 ATA 地址并构建 CreateIdempotent 指令
2. `BuildMessage`：构建 TransferChecked 交易并序列化 Message
3. `VerifySignature`：Ed25519 签名验签成功，篡改 Message 后验签失败
4. `DecodeTransaction`：解析 Raw Tx，还原 Source / Destination / Mint / Owner / Amount（`1500000`）/ Decimals（`6`）

## test3：BTC 跨链转账（Solidity + Go + BTC + MPC）

```bash
cd test3
go test -v ./...   # 单元测试
go run .           # 运行 Demo
```

**测试预期**：`internal/listener` 下 3 个测试均 `PASS`：

| 测试 | 验证内容 |
| --- | --- |
| `TestListenerScenarios` | 各场景最终状态正确，重复事件只转一次 BTC |
| `TestRetryAfterStartFailure` | `startBTCTransfer` 失败后重新投递事件，可继续完成转账 |
| `TestContextCancel` | 等待确认时 ctx 取消立即返回 |

**Demo 预期**：日志展示完整流程（合约事件 → Go 监听 → 构建 BTC 交易 → Mock MPC 签名 → 验签与脚本校验 → 广播 → 回调合约），最后输出：

```text
== 最终合约状态
  request 1: Completed
  request 2: Failed
  request 3: Pending
```

`main.go` 依次投递 4 个场景：

| 场景 | 预期结果 |
| --- | --- |
| 正常请求（测试网地址） | `request 1` 完成 BTC 转账，状态 Completed |
| 同一事件重复投递 | 按 `requestId` 去重，不会重复转 BTC |
| 测试网请求中使用主网地址 | 签名前被拦截，`request 2` 标记 Failed |
| Reorg 移除的日志（`Removed=true`） | 被忽略，`request 3` 保持 Pending |

合约代码见 `test3/contracts/BTCTransfer.sol`（未包含 Solidity 编译/部署脚本，链上行为由 Go 中的 `MockBackend` 模拟）。各 Mock 说明见 [test3/README.md](test3/README.md)。
