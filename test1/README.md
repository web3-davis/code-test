# Ethereum 入金检测示例

使用 Go 和 `go-ethereum` 检测三种以太坊入金：普通 ETH、ERC20 Token、合约内部 ETH 转账。

> **定位**：仅供学习和代码演示，不可直接用于生产环境资金入账。

## 文件结构

```text
test1/
├── main.go                  # 公共配置、Deposit 结构、ScanBlocks 区块扫描
├── eth_deposit.go           # CheckETHDeposit
├── erc20_deposit.go         # CheckERC20Deposits
├── internal_eth_deposit.go  # CheckInternalETHDeposits
├── main_test.go             # 基于模拟 RPC 的单元测试
└── README.md
```

`go.mod` 位于上级目录（模块名 `risk`），包路径为 `risk/test1`。

## 功能

| 函数 | 检测对象 | 核心校验 |
| --- | --- | --- |
| `CheckETHDeposit` | 顶层 ETH 转账 | `tx.To()`、`tx.Value()`、Receipt 成功、平台地址 |
| `CheckERC20Deposits` | USDT、USDC 等 ERC20 转账 | Receipt 成功、Token 合约白名单、`Transfer` 日志、平台地址 |
| `CheckInternalETHDeposits` | 合约内部 ETH 转账 | Receipt 成功、`callTracer`、调用未回滚、接收地址 |
| `ScanBlocks` | 区块区间 `[from, to]` | 校验 ChainID 为 1，逐块取交易并依次调用以上三个函数 |

前两项使用 `ethclient.Client`，内部 ETH 检测额外需要支持 `debug_traceTransaction` 的 `rpc.Client`。一笔交易可能存在多笔 ERC20 或内部 ETH 入金，故对应函数返回切片。

## 环境准备

- Go（版本以本地 `go.mod` 和依赖要求为准）
- 可访问的以太坊 JSON-RPC 节点
- 内部 ETH 检测需要节点支持 `debug_traceTransaction` 和 `callTracer`

安装依赖：

```bash
go mod tidy
```

代码是一个 **Go package**（`package test1`），没有 `main` 函数，不能直接 `go run`；由调用方传入节点客户端和区块区间，调用 `ScanBlocks` 扫描。

## 基础配置

在 `main.go` 中修改以下 Map：

- `DepositAddresses`：充值地址 → 用户 ID。
- `SupportedTokens`：ERC20 合约地址 → 币种名称，示例为以太坊主网 USDT/USDC。

这些 Map 仅用于模拟数据库。扫描过程中不要并发修改 Map；真实系统应使用持久化存储与明确的地址归属约束。

## 调用示例

扫描一个区块区间：

```go
ctx := context.Background()
rawRPC, err := rpc.DialContext(ctx, "https://your-ethereum-rpc")
if err != nil { return err }
defer rawRPC.Close()
client := ethclient.NewClient(rawRPC)

deposits, err := test1.ScanBlocks(ctx, client, rawRPC, 21000000, 21000010)
if err != nil { return err }
for _, d := range deposits {
	fmt.Printf("%+v\n", d)
}
```

也可以对单笔交易直接调用三个检测函数：

```go
ethDeposit, err := test1.CheckETHDeposit(ctx, client, tx)
erc20Deposits, err := test1.CheckERC20Deposits(ctx, client, tx)
internalDeposits, err := test1.CheckInternalETHDeposits(ctx, client, rawRPC, tx)
```

`ScanBlocks` 遇到任一错误立即返回（错误中带区块号或交易哈希）。若节点不支持 Trace，应明确处理该错误，不得将未检测视为无内部入金。

`ScanBlocks` 是简易实现：每笔交易会被三个函数各请求一次 Receipt，并对每笔交易调用 `debug_traceTransaction`，扫描较慢；同一交易可能同时产生顶层 ETH 与内部 ETH 两类入金记录。

## 返回数据

三个函数使用统一的 `Deposit` 结构：

| 字段 | 含义 |
| --- | --- |
| `UserID` | 充值地址所属用户 |
| `Asset` | ETH、USDT、USDC 等 |
| `To` | 实际接收地址 |
| `Amount` | 最小单位整数：ETH 使用 Wei，ERC20 使用 Token 原始单位 |
| `TxHash` | 交易哈希 |
| `Source` | `ETH` / `ERC20` / `INTERNAL_ETH` |
| `EventIndex` | 顶层交易为 `top`，ERC20 为 `log:<index>`，内部转账为 `trace:<path>` |

没有识别到充值时，ETH 函数返回 `nil, nil`，另外两个函数返回空切片（可能为 `nil`）和 `nil` 错误。RPC 或解析失败返回 `error`，**不可按无充值处理**。

## 单元测试

```bash
go test -v ./test1/
```

`main_test.go` 不连接真实节点：通过 `rpc.NewServer` + `rpc.DialInProc` 在进程内模拟 `eth_chainId`、`eth_getBlockByNumber`、`eth_getTransactionReceipt`、`debug_traceTransaction`，构造一个包含 4 笔已签名交易的区块：

| 交易 | 场景 | 预期 |
| --- | --- | --- |
| txETH | 直接向 user_001 转 1000 wei | `ETH` / `top` |
| txERC20 | USDT `Transfer` 200 给 user_002 | `ERC20` / `log:7` |
| txInternal | 合约内部转 5 wei 给 user_001，另一笔转 user_002 的子调用回滚 | 仅 `INTERNAL_ETH` / `trace:0` |
| txFailed | Receipt 失败的交易 | 不入账 |

## 推荐处理流程

```text
可信节点获取已上链交易
    ↓
执行三类入金检测
    ↓
校验 ChainID、区块归属和确认/最终性
    ↓
按 (ChainID, TxHash, EventIndex) 幂等保存
    ↓
数据库事务完成入账
```

注意：入金检测不等于资金已入账。最终入账、Reorg 处理、重试及余额更新均需由外层系统实现。

## 安全边界与已知限制

1. **链与区块验证**：`ScanBlocks` 仅校验 ChainID 为 1；直接调用检测函数时不校验 ChainID。代码均不处理确认数、区块最终性或 Reorg，调用方必须完成这些校验。
2. **ERC20 白名单**：仅信任指定合约发出的标准 `Transfer` 日志，不按代币名称识别；非标准、手续费型、Rebase 或恶意代币需要额外核对实际余额。
3. **内部 ETH Trace**：代码遍历成功的 `CALL` / `SELFDESTRUCT` 子调用并跳过失败调用。不同节点/Tracer 对 `SELFDESTRUCT` 的输出可能不同；必须针对目标节点验证，不能保证覆盖所有价值转移路径。
4. **内部划转**：未过滤平台自有地址之间的转账；真实账务系统必须防止平台内部转移被误当作新增资产。
5. **幂等性**：仅提供 `EventIndex`，并未实现数据库唯一索引或事务；同一交易可能被重复检测。
6. **安全性**：本示例仅有覆盖基础场景的单元测试，未实现生产所需的完整异常处理、监控、对账、资产审核及高可用 RPC 策略。

## 建议测试场景

- 正常 ETH、ERC20、内部 ETH 入金
- 非平台充值地址与假 USDT 合约
- 失败交易、失败内部调用与父调用回滚
- 同一交易的多条 `Transfer` 日志或多次内部转账
- 重复扫描、RPC 超时、Trace 不可用
- 区块重组及平台地址之间的内部转账
