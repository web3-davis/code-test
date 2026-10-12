# Solana SPL Token 交易 Demo

用 Go + [solana-go](https://github.com/gagliardetto/solana-go) v1.12.0 演示 SPL Token 交易的四个核心步骤：创建 ATA、构建转账 Message、Ed25519 签名验签、反解析 Raw Transaction。

全部使用本地模拟数据，不连接 RPC，不广播交易。

## 运行

在仓库根目录执行（`go.mod` 位于根目录）：

```bash
go run ./test2
```

输出分为 4 段，分别对应 4 个函数的结果。

## 文件结构

| 文件 | 函数 | 作用 |
| --- | --- | --- |
| `prepare_account.go` | `PrepareAccount` | 计算 ATA 地址，构建 CreateIdempotent 指令 |
| `build_message.go` | `BuildMessage` | 构建 TransferChecked 交易，序列化 Message |
| `verify_signature.go` | `VerifySignature` | Ed25519 签名、验签、篡改测试 |
| `decode_transaction.go` | `DecodeTransaction` | 解码 Hex/Base64 Raw Tx，解析字段和指令 |
| `main.go` | `main` | 串联以上四步 |

## 原理

### 1. ATA（Associated Token Account）

- SPL Token 余额不存放在钱包地址上，而是存放在独立的 Token Account 里。ATA 是每个 `(owner, mint)` 对应的“默认” Token Account。
- ATA 是 **PDA**（Program Derived Address）：
  `FindProgramAddress(seeds=[owner, TokenProgramID, mint], programID=ATokenGP...)`
  PDA 不在 Ed25519 曲线上，因此**没有私钥**，只能由程序代为签名。
- 创建指令的账户顺序为：`payer(签名、可写)`、`ata(可写)`、`owner`、`mint`、`SystemProgram`、`TokenProgram`。
- 指令数据：`0` = Create，`1` = **CreateIdempotent**。ATA 已存在时，Idempotent 版本不会报错。SDK 只提供了 Create，所以这里用 `solana.NewInstruction` 自己构造。

### 2. TransferChecked 与 Message

- `TransferChecked` 比 `Transfer` 多传了 `mint` 和 `decimals`，链上会校验两者，防止转错币或搞错精度。
  - 账户：`source`、`mint`、`destination`、`owner(签名)`
  - 数据：`[12] + amount(u64 LE) + decimals(u8)`
- 示例金额为 `1_500_000`，`decimals=6`，即 1.5 个 Token。
- Legacy **Message** 的结构：

  ```
  Header(3 字节: 签名数, 只读签名数, 只读非签名数)
  AccountKeys(去重并排序: 签名可写 → 签名只读 → 非签名可写 → 非签名只读)
  RecentBlockhash(32 字节，用于防重放，约 150 个区块后过期)
  Instructions(programIdIndex + 账户索引列表 + data)
  ```

- **Fee Payer 固定为 `AccountKeys[0]`**。指令中只保存账户的**索引**，不保存完整公钥。

### 3. 签名

- Solana 使用 **Ed25519**，签名对象是**序列化后的 Message 字节**，不是整笔交易。
- Solana 私钥共 64 字节（32 字节 seed + 32 字节公钥），与 Go 标准库 `ed25519.PrivateKey` 的格式一致，可以直接使用。
- 只要改动 Message 中的任意一个字节，验签就会失败。这保证了交易内容（金额、收款人、blockhash 等）无法被篡改。

### 4. Raw Transaction

```
Raw Tx = compact-u16(签名数) + 签名[64 字节] × N + Message
```

- 第 i 个签名对应 `AccountKeys[i]`。签名数量由 `Header.NumRequiredSignatures` 决定。
- 解析流程：先做 Hex/Base64 解码，再调用 `TransactionFromBytes` 反序列化，然后用 `ProgramIDIndex` 找到程序。如果是 Token Program，就调用 `token.DecodeInstruction`，从中还原 TransferChecked 的各个字段。
- 只能从交易中拿到 **Token Account**。如果要查它的 owner 钱包，需要通过 RPC 读取账户数据，本 Demo 不做这一步。

## 范围说明

- 只处理 Legacy Transaction，不处理 Versioned Transaction / Address Lookup Table。
- 不支持多签、Token-2022。
- ATA 创建指令仅用于演示，没有放进转账交易。真实场景中，如果收款方 ATA 可能不存在，通常会把 CreateIdempotent 和 TransferChecked 放进同一笔交易。
