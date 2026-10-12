# 第三道题：Solidity + Go + BTC + MPC 跨链转账开发方案

## 一、项目目标

实现一个基于 Solidity 智能合约和 Go 后台服务的 BTC 转账 Demo。

业务流程：

1. 用户调用 Solidity 合约申请 BTC 转账。
2. 合约记录请求并抛出事件，包含 BTC 接收地址和转账金额。
3. Go 后台监听合约事件。
4. Go 后台构建 Bitcoin 交易。
5. 调用 MPC 签名机完成交易签名。
6. 广播 Bitcoin 交易。
7. 确认 BTC 交易成功后，Go 后台调用 Solidity 合约。
8. Solidity 更新转账状态，并抛出转账完成事件。

这是面试演示项目，重点展示业务逻辑和系统设计，不要求连接真实区块链运行。

## 二、技术选型

- Solidity：智能合约
- Go：后台服务
- go-ethereum：监听合约事件、调用合约
- btcsuite：构建和序列化 BTC 交易
- MPC：通过接口模拟签名机调用
- OpenZeppelin：Solidity 权限控制和安全组件

要求：

- 不需要数据库。
- 不需要 HTTP API。
- 不需要前端。
- 不需要 Hardhat、Foundry。
- 不需要 Docker。
- 不需要真实广播。
- 不需要真正部署 MPC 集群。
- 不使用真实 BTC 或真实钱包私钥。
- 优先实现核心代码，不要过度设计。

## 三、项目目录

所有代码放在 `test3/` 目录下，不需要修改上层目录结构。

```text
test3/
├── contracts/
│   └── BTCTransfer.sol
├── internal/
│   ├── listener/
│   │   └── event_listener.go
│   ├── bitcoin/
│   │   └── transfer.go
│   ├── mpc/
│   │   └── signer.go
│   └── ethereum/
│       └── notifier.go
├── main.go
├── go.mod
└── README.md
```

各模块职责明确，尽量避免跨模块耦合。

## 四、Solidity 智能合约

文件：`contracts/BTCTransfer.sol`

### 1. 核心数据结构

定义转账请求：

- requestId：唯一请求 ID
- requester：申请人
- btcAddress：BTC 接收地址
- amount：BTC 转账金额，单位 satoshi
- btcTxHash：BTC 交易 Hash
- status：转账状态

状态枚举：

- Pending：等待处理
- Processing：处理中
- Completed：已完成
- Failed：失败

### 2. 申请 BTC 转账

函数：`requestBTCTransfer`

参数：

- btcAddress
- amount

逻辑：

1. 检查系统是否暂停。
2. 检查 BTC 地址不为空。
3. 检查金额大于 0。
4. 生成唯一 requestId。
5. 保存转账请求。
6. 设置状态为 Pending。
7. 抛出 BTCTransferRequested 事件。

事件包含：

- requestId
- requester
- btcAddress
- amount

注意：合约只记录请求，不直接操作 Bitcoin 网络，也不持有 BTC。

### 3. 开始处理转账

函数：`startBTCTransfer`

参数：

- requestId

逻辑：

1. 仅授权后台角色可以调用。
2. 检查请求存在。
3. 检查状态为 Pending。
4. 更新状态为 Processing。
5. 抛出 BTCTransferProcessing 事件。

### 4. 通知 BTC 转账完成

函数：`completeBTCTransfer`

参数：

- requestId
- btcTxHash

逻辑：

1. 仅授权后台角色可以调用。
2. 检查请求存在。
3. 检查状态为 Processing。
4. 检查 btcTxHash 不为空。
5. 保存 BTC 交易 Hash。
6. 更新状态为 Completed。
7. 抛出 BTCTransferCompleted 事件。

事件包含：

- requestId
- btcTxHash
- amount
- btcAddress

### 5. 转账失败处理

函数：`failBTCTransfer`

参数：

- requestId
- reason

逻辑：

1. 仅授权后台角色可以调用。
2. 检查请求状态。
3. 更新为 Failed。
4. 抛出 BTCTransferFailed 事件。

注意：

- 不允许已经 Completed 的请求变成 Failed。
- BTC 交易广播后如果状态未知，不能直接标记 Failed 后重新发送，避免重复转账。
- 已广播但未确认的交易，应保持 Processing 状态。

### 6. 安全和权限控制

使用 OpenZeppelin：

- AccessControl：角色权限控制
- Pausable：紧急暂停
- ReentrancyGuard：防止重入

角色：

- DEFAULT_ADMIN_ROLE：管理员
- OPERATOR_ROLE：后台服务

其他要求：

- 只有管理员可以授权或撤销 Operator。
- 只有管理员可以暂停和恢复合约。
- 普通用户只能创建转账请求和查询状态。
- 已完成的请求不能重复完成。
- 每个请求必须有唯一 requestId。
- 提供查询请求详情的函数。

特别说明：

- 合约不需要接收 ETH。
- 不要实现 ETH 提现功能。
- BTC 地址的完整有效性校验交给 Go 后台。
- Solidity 只做基本参数检查。
- 因为合约无法独立验证 BTC 链状态，Completed 依赖授权后台的可信通知。

## 五、Go 监听合约事件

文件：`internal/listener/event_listener.go`

核心函数：`ListenBTCTransferEvents`

实现逻辑：

1. 订阅 Solidity 合约的 BTCTransferRequested 事件。
2. 解析 requestId、BTC 地址和金额。
3. 获取事件所属区块、交易 Hash 和 LogIndex。
4. 等待足够的 EVM 链确认，避免 Reorg。
5. 调用合约的 startBTCTransfer，将状态更新为 Processing。
6. 调用 BitcoinTransfer 函数。
7. BTC 确认成功后调用 completeBTCTransfer。
8. 出现可确定的业务错误时，按状态处理失败。

要求：

- 使用 go-ethereum。
- 事件监听代码尽量简单。
- 可以使用模拟事件代替真实 WebSocket 订阅。
- 保留真实事件订阅的代码示例。
- 处理 Removed Log（Reorg）。
- 同一 requestId 不能重复触发 BTC 转账。
- 演示用内存 Map 做去重即可，不使用数据库。

注意：内存 Map 只能演示幂等，不能保证服务重启后的资金安全。真实生产环境需要持久化任务状态和可靠的重复处理保护。

## 六、BTC 交易构建与广播

文件：`internal/bitcoin/transfer.go`

核心函数：`BitcoinTransfer`

参数：

- btcAddress：接收方 BTC 地址
- amount：金额，单位 satoshi
- signer：MPC 签名接口

返回：

- btcTxHash
- error

实现逻辑：

### 1. 校验参数

- 检查 BTC 地址格式。
- 检查 BTC 网络类型。
- 检查 amount > 0。
- 检查金额是否满足 Dust 限制。

### 2. 构建 BTC 交易

使用 btcsuite：

1. 获取模拟 UTXO 列表。
2. 选择足够支付金额和手续费的 UTXO。
3. 创建 Bitcoin Transaction。
4. 添加 TxIn。
5. 添加接收方 TxOut。
6. 计算手续费。
7. 添加找零 TxOut。
8. 为每个输入构建对应的签名摘要。

建议使用最简单的原生 SegWit P2WPKH 交易。

暂不实现：

- Taproot
- 多签脚本
- RBF
- CPFP
- 复杂 UTXO 选择策略

### 3. 调用 MPC 签名机

1. 构建待签名的交易摘要。
2. 将摘要、签名算法及密钥标识提交给 MPC Signer。
3. 获取签名结果。
4. 验证签名是否有效。
5. 构建 Witness。
6. 将签名填入 BTC 交易。

要求：

- 使用 secp256k1 ECDSA 签名。
- MPC 签名机必须返回 BTC 可使用的签名格式。
- 使用公钥验证签名。
- 正确处理 DER 编码和 SIGHASH 类型。
- 不允许在业务服务中持有完整私钥。

### 4. 广播交易

1. 将完整 BTC 交易序列化。
2. 计算 Transaction ID。
3. 调用广播接口。
4. 返回交易 Hash。

Demo 中：

- 广播接口使用 Mock 实现。
- 打印 Raw Transaction 和 TxHash。
- 不调用真实 Bitcoin 节点。

注意：广播成功不代表链上确认成功，不能立即通知 Solidity 转账 Completed。

## 七、MPC 签名模块

文件：`internal/mpc/signer.go`

核心接口：

```go
type Signer interface {
    Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error)
    PublicKey(ctx context.Context, keyID string) ([]byte, error)
}
```

实现一个 `MockMPCSigner`。

要求：

1. 业务服务只通过 Signer 接口请求签名。
2. 使用 keyID 标识签名密钥。
3. 不传输完整私钥。
4. 不在业务服务中拼接私钥分片。
5. Mock 实现返回可用于演示流程的模拟签名结果。
6. 在代码注释中明确说明 Mock 不等于真实 MPC。

真实 MPC 的逻辑：

- 多个参与方分别持有私钥份额。
- 每个参与方执行分布式签名协议。
- 最终组合得到 ECDSA 签名。
- 签名过程中不恢复完整私钥。

本项目不需要实现复杂的 MPC 密码学协议，只保留签名机接口和模拟调用流程。

如果 Mock 没有真实密码学签名能力，应明确区分模拟结果与可通过密码学验证的签名，不得伪装成有效签名。

## 八、Go 通知 Solidity 合约

文件：`internal/ethereum/notifier.go`

实现以下函数：

### 1. StartTransfer

参数：

- requestId

调用合约：

`startBTCTransfer(requestId)`

### 2. CompleteTransfer

参数：

- requestId
- btcTxHash

调用合约：

`completeBTCTransfer(requestId, btcTxHash)`

### 3. FailTransfer

参数：

- requestId
- reason

调用合约：

`failBTCTransfer(requestId, reason)`

要求：

- 使用 go-ethereum。
- 演示 ABI 编码和合约调用。
- 可以使用 Mock 模拟发送交易。
- 不需要真实 ETH 支付 Gas。
- 明确区分交易已提交、交易上链成功、合约执行成功。

## 九、main.go 演示流程

使用 Mock 数据完整模拟一次跨链 BTC 转账：

1. 模拟 Solidity 合约产生 BTCTransferRequested 事件。
2. Go Listener 解析事件。
3. 检查请求 ID 和 BTC 地址。
4. 模拟将请求状态更新为 Processing。
5. 获取模拟 UTXO。
6. 构建 BTC 转账交易。
7. 调用 MockMPCSigner。
8. 构建签名后的 BTC 交易。
9. 调用 Mock Broadcaster 广播。
10. 模拟 BTC 网络确认成功。
11. 调用 CompleteTransfer 通知 Solidity。
12. Solidity 更新 Completed 并抛出完成事件。
13. 打印完整业务流程。

所有流程在本地运行，不需要真实区块链环境。

## 十、重要安全要求

### 1. 防止重复 BTC 转账

- 使用 requestId 作为业务唯一标识。
- 同一请求不能重复转账。
- 不允许 Completed 状态再次执行。
- 交易状态未知时不能简单重试构建新交易。

### 2. 防止未经授权的转账

- Solidity 使用 Operator 角色控制完成通知。
- Go 后台必须校验事件来源合约地址和 ChainID。
- MPC Signer 应具有独立授权策略。

特别注意：

Solidity 的转账申请事件本身不代表 BTC 资金已经获得授权。

本 Demo 可以假设请求已经通过业务授权，但真实系统必须验证用户 BTC 资产余额、提款额度和资金归属，不能仅凭任何用户发出的事件就动用平台 BTC。

### 3. BTC 签名安全

- 不使用真实私钥。
- 不记录私钥或密钥份额日志。
- 业务服务不能获取完整私钥。
- MPC 签名必须绑定已审核的交易内容。
- 签名前校验收款地址、金额、手续费和找零地址。

### 4. 跨链状态一致性

- BTC 广播成功不等于确认成功。
- BTC 达到设定确认条件后再通知完成。
- EVM 合约通知失败时，应重试通知，而不是重新发送 BTC。
- Demo 可以模拟状态和重试流程。

## 十一、验收标准

1. Solidity 能创建 BTC 转账请求并抛出事件。
2. Solidity 有完善的权限控制和状态管理。
3. Go 能正确解析申请事件。
4. Go 能构建 P2WPKH Bitcoin 交易。
5. Go 能通过 Signer 接口请求 MPC 签名。
6. Mock 签名与真实 MPC 的边界明确。
7. Go 能序列化交易并模拟广播。
8. Go 能模拟 BTC 确认后通知 Solidity。
9. Solidity 能更新 Completed 并抛出完成事件。
10. 所有模块职责清晰，代码简洁，关键逻辑有注释。

## 十二、开发原则

这是一个 AI Coding 面试题，重点是逻辑，不是生产部署。

请遵循：

- 代码优先，少写解释。
- 每个文件只负责一个明确模块。
- 尽量少封装，避免过度设计。
- 允许 Mock 外部区块链和 MPC 服务。
- 优先使用成熟 SDK，不重复实现底层密码学。
- 代码需要清晰展示真实业务流程。
- 不要新增与题目无关的功能。
- 不要生成 Hardhat、Foundry、Docker 等脚手架。
- 提供简洁 README，说明如何运行及哪些部分为 Mock。

**请直接在 test3/ 目录中完成以上代码。**