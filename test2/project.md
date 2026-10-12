# Solana SPL Token 交易开发方案

## 一、项目目标

使用 Go 实现 Solana SPL Token 交易的四个核心功能：

1. 创建 SPL Token 转账交易 Message。
2. 使用 Ed25519 对 Message 签名并验证。
3. 反序列化 Raw Transaction，解析交易内容。
4. 创建 ATA（Associated Token Account）的初始化指令。

本项目用于展示 Solana 交易原理，不要求真实链上执行。

## 二、开发要求

- 语言：Go
- SDK：推荐 github.com/gagliardetto/solana-go
- 可以使用第三方 SDK。
- 不需要数据库、RPC、HTTP API。
- 不需要广播交易。
- 不需要真实 SOL 或 SPL Token。
- 可以使用模拟的公私钥、Mint、Blockhash。
- 只需要实现核心逻辑，尽量使用简单代码。
- 可以使用 SDK 的实际函数，也可以用伪代码表达部分步骤。
- 重点是代码逻辑清晰、容易阅读和理解。
- 四个核心函数分别放到四个文件中。
- 不要引入复杂的架构设计。

## 三、项目结构

```text
test2/
├── main.go
├── prepare_account.go
├── build_message.go
├── verify_signature.go
└── decode_transaction.go
```

## 四、四个核心函数

### 1. PrepareAccount：创建 ATA

文件：prepare_account.go

函数：PrepareAccount

参数：

- owner：钱包公钥
- mint：SPL Token Mint 地址
- payer：支付账户创建费用的钱包公钥

实现逻辑：

1. 根据 owner 和 mint 计算 ATA 地址。
2. 使用 Associated Token Program 派生 PDA。
3. 构建 Create Associated Token Account 指令。
4. 使用 Idempotent 创建指令，避免账户已存在时创建失败。
5. 返回 ATA 地址和创建指令。

注意：

- ATA 是 PDA，不需要生成新的私钥。
- 本函数只构建创建指令，不真正创建账户。
- 不查询账户是否存在。
- 不需要连接 Solana RPC。

### 2. BuildMessage：构建 SPL Token 转账 Message

文件：build_message.go

函数：BuildMessage

参数：

- sender：发送方钱包公钥
- senderTokenAccount：发送方 Token Account
- receiverTokenAccount：接收方 Token Account
- mint：Token Mint
- amount：转账金额（最小单位）
- decimals：Token 精度
- recentBlockhash：模拟区块哈希

实现逻辑：

1. 构建 SPL Token TransferChecked 指令。
2. 指定发送方 Token Account、接收方 Token Account、Mint、金额及精度。
3. 设置发送方为 Token Account 的授权签名者（假设其为 owner）。
4. 设置交易 Fee Payer。
5. 使用 recentBlockhash 构建 Transaction。
6. 提取并序列化 Message。
7. 返回 Message 和 Transaction。

注意：

- 使用 TransferChecked，而不是普通 Transfer。
- 不需要发送交易。
- Blockhash 可以使用合法格式的模拟值。
- 暂不考虑多签、Token-2022、额外扩展。

### 3. VerifySignature：交易签名与验证

文件：verify_signature.go

函数：VerifySignature

参数：

- message：序列化后的交易 Message
- privateKey：Ed25519 私钥

实现逻辑：

1. 使用私钥对 Message 字节进行 Ed25519 签名。
2. 从私钥推导公钥。
3. 使用公钥验证签名。
4. 修改 Message 中的一个字节，再次验证。
5. 证明修改后的 Message 验签失败。
6. 返回 Signature 和验证结果。

注意：

- Solana 使用 Ed25519 签名算法。
- 签名对象是序列化后的 Message 字节。
- 不需要模拟钱包连接。
- 不要使用真实资金钱包的私钥。

### 4. DecodeTransaction：反解析 Raw Transaction

文件：decode_transaction.go

函数：DecodeTransaction

参数：

- rawTx：序列化交易的 Hex 或 Base64 字符串

实现逻辑：

1. 将 Hex 或 Base64 解码成字节数组。
2. 使用 Solana SDK 反序列化 Transaction。
3. 解析交易签名列表。
4. 解析 Fee Payer。
5. 解析 RecentBlockhash。
6. 解析 Account Keys。
7. 解析 Instructions。
8. 识别 SPL Token TransferChecked 指令。
9. 还原 Token Account、Mint、Amount、Decimals 等信息。
10. 返回结构化解析结果。

注意：

- 必须区分 Message 与完整 Transaction。
- Raw Transaction 包含签名和 Message。
- Legacy Transaction 优先，暂不处理 Versioned Transaction。
- 不通过 RPC 查询账户信息。
- 不要求一定能从 Token Account 还原钱包 Owner。
- 不需要广播交易。

## 五、main.go 演示流程

使用本地模拟数据串联四个函数：

1. 生成模拟钱包密钥。
2. 创建模拟 Token Mint 和 RecentBlockhash。
3. 调用 PrepareAccount，获取接收方 ATA 和创建指令。
4. 调用 BuildMessage，生成 SPL Token 转账 Message。
5. 调用 VerifySignature，对 Message 签名并验证。
6. 将签名填入 Transaction 的对应签名槽位。
7. 将完整 Transaction 序列化为 Base64。
8. 调用 DecodeTransaction，解析刚才构建的交易。
9. 打印交易解析结果。

注意：ATA 创建指令仅用于演示初始化逻辑。因为没有真实链上账户，本例不验证 ATA 是否已经存在。

## 六、验收标准

1. PrepareAccount 能计算 ATA 地址并构建创建指令。
2. BuildMessage 能构建合法的 SPL Token TransferChecked Message。
3. VerifySignature 能完成 Ed25519 签名、验签及消息篡改测试。
4. DecodeTransaction 能反解析本地构建的交易。
5. main.go 能完整演示四个函数的调用。
6. 代码结构简单，每个函数职责明确。
7. 不实现与题目无关的业务逻辑。

## 七、特别要求

这是一个用于面试学习的代码 Demo，而不是生产级 Solana 钱包。

请优先保证：

- 逻辑正确。
- 代码简洁。
- 函数独立。
- 注释清楚。
- 便于面试时讲解。

不要额外实现数据库、RPC 服务、区块监听、余额查询、交易广播、复杂接口抽象或其他非必要功能。

优先使用 SDK 的真实 API。如果 SDK 版本中的 API 与示例不一致，以实际安装版本为准。

除了必要的第三方 SDK 调用，尽量不要编写大量底层序列化代码。