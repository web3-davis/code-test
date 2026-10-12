# 题目

## 场景
现在有一个虚拟币钱包的入金系统，主要包括以下三种入金类型：
- 以太坊本币入金（ETH）
- erc20 token 入金（类似于 USDT、USDC 等）
- 合约本币入金

## 要求：
1. 三种类型都要防范假入金
2. 要保证入金地址的准确性


-------- 以下是开发方案 -----------------


## 开发方案

### 一、项目目标

使用 Go 实现一个基于 Ethereum 的钱包入金监听系统，支持三种入金类型：

1. **ETH 本币入金**：用户直接向钱包地址转入 ETH。
2. **ERC20 Token 入金**：用户向钱包地址转入 USDT、USDC 等代币。
3. **合约本币入金**：用户通过智能合约向钱包地址转入 ETH（Internal Transaction）。

核心要求：

- 防范假入金。
- 确保入金地址准确。
- 避免重复入账。
- 处理区块链重组（Reorg）。
- 保证资金入账安全。

### 二、技术选型

- 开发语言：Go
- 区块链 SDK：go-ethereum
- 数据库：MySQL
- 链接方式：Ethereum JSON-RPC
- 测试：Go testing、模拟 RPC

优先使用标准库和 go-ethereum，保持代码简单、模块清晰。

### 三、系统架构

系统主要划分为以下模块：

#### 1. 区块扫描模块（BlockScanner）

- 按区块高度顺序扫描 Ethereum 区块。
- 获取区块及交易数据。
- 对每笔交易获取 Transaction Receipt。
- 通过 Trace API 获取合约内部 ETH 转账。
- 保存已扫描区块高度。
- 支持程序重启后继续扫描。

#### 2. 入金解析模块（DepositParser）

按照入金类型分别解析：

**ETH 本币入金**

- 检查交易接收地址 `tx.To()`。
- 检查交易金额 `tx.Value()`。
- 检查 Receipt 状态为成功。
- 检查目标地址是否属于平台。

**ERC20 Token 入金**

- 解析 Receipt 中的 `Transfer` 事件。
- 校验事件所属合约地址是否在支持的 Token 白名单。
- 提取 `from`、`to`、`value`。
- 检查 `to` 是否属于平台地址。
- 不依赖 Token 名称、Symbol 判断资产身份。

**合约本币入金**

- 使用 `debug_traceTransaction` 或节点支持的等效 Trace API。
- 提取执行成功的内部 ETH 转账。
- 检查接收地址是否属于平台。
- 检查 ETH 实际转账金额。
- 排除失败或已回滚的内部调用。
- 避免将交易顶层转账重复解析为内部入金。

#### 3. 地址管理模块（AddressManager）

- 数据库保存用户与入金地址的绑定关系。
- 地址统一转换为 20 字节 Ethereum Address 进行比较。
- 必须从可信数据库查询地址归属。
- 地址不存在或不属于平台时不入账。
- 同一链上地址不能同时绑定不同用户。
- 禁止仅通过地址前缀、字符串相似度判断归属。

#### 4. 入金确认模块（DepositConfirmer）

- 发现入金后先记录为 `PENDING`。
- 等待配置的区块确认数。
- 确认交易仍属于规范链。
- 满足条件后标记为 `CONFIRMED`。
- 发现 Reorg 时撤销未最终入账的记录并重新扫描。

#### 5. 账务模块（DepositLedger）

- 只有确认成功且所有校验通过的入金才能记账。
- 使用数据库事务完成入金状态变更和余额增加。
- 使用唯一入金 ID 保证幂等。
- 同一笔入金不允许重复增加余额。
- 金额使用最小单位整数表示，禁止使用 float。

### 四、防假入金规则

| 入金类型 | 核心校验 |
|---|---|
| ETH 本币 | Receipt 成功、接收地址正确、金额大于 0、区块确认 |
| ERC20 Token | Receipt 成功、真实合约地址白名单、Transfer 事件、接收地址正确、区块确认 |
| 合约本币 | Trace 执行成功、内部转账未回滚、接收地址正确、金额大于 0、区块确认 |

额外安全要求：

1. 不信任用户提交的交易 Hash、金额或 Token 信息，所有数据从链上获取。
2. 不通过 Token Symbol 判断币种，必须使用 ChainID + Token 合约地址。
3. 不把 ERC20 合约调用的输入参数当作入金凭证。
4. 不把 ETH 转入合约的交易当作用户地址的入金。
5. 不认可失败交易、失败内部调用或未确认交易。
6. RPC 返回异常、Trace 不完整时暂停该区块的确认处理，不能默认成功。
7. 防止重复扫描、重复事件和重复账务处理。

### 五、数据库设计

#### 1. deposit_addresses（入金地址）

- id
- user_id
- chain_id
- address

唯一索引：`(chain_id, address)`

#### 2. supported_assets（支持的资产）

- id
- chain_id
- asset_type：NATIVE / ERC20
- contract_address：ETH 为空
- symbol
- decimals
- enabled

唯一标识：`chain_id + asset_type + contract_address`

#### 3. deposits（入金记录）

- id
- user_id
- chain_id
- asset_id
- tx_hash
- event_index：区分同一交易中的多笔入金
- to_address
- amount
- block_number
- block_hash
- status：PENDING / CONFIRMED / ORPHANED
- created_at
- updated_at

唯一索引：`(chain_id, tx_hash, event_index)`

其中 event_index 应采用统一设计：

- ETH 顶层转账使用固定标识。
- ERC20 使用 Receipt LogIndex。
- 内部 ETH 转账使用稳定且唯一的 Trace 路径。

不同入金来源必须有互不冲突的唯一标识。

#### 4. ledger_entries（账务流水）

- id
- deposit_id
- user_id
- asset_id
- amount
- created_at

唯一索引：`deposit_id`

### 六、入金处理流程

1. 扫描新区块。
2. 获取交易、Receipt 和 Trace 数据。
3. 解析三类入金。
4. 检查地址归属和资产白名单。
5. 校验交易执行结果和入金金额。
6. 保存 PENDING 入金记录。
7. 等待区块确认并检查 Reorg。
8. 满足条件后使用数据库事务入账。
9. 标记 CONFIRMED，推进扫描进度。

### 七、测试要求

使用模拟链上数据完成自动化测试，至少覆盖：

1. 正常 ETH 入金。
2. 正常 ERC20 入金。
3. 正常合约内部 ETH 入金。
4. 假 USDT 合约冒充真实 USDT。
5. ERC20 Transfer 输入参数伪造但无真实事件。
6. 交易失败但存在调用数据或日志痕迹。
7. 内部 ETH 转账执行失败或回滚。
8. 入金地址不属于平台。
9. 同一交易包含多笔入金。
10. 重复扫描同一区块和交易。
11. 区块确认数不足。
12. 区块链 Reorg。
13. 多个 Worker 并发入账。
14. RPC 超时或 Trace 不可用。

重点验证：**任何失败、伪造、重复、未确认的入金都不能增加用户余额。**

### 八、开发要求

1. 先实现可运行的最小版本，不需要 HTTP API 和前端。
2. 使用清晰的接口隔离 RPC、解析、存储和账务逻辑。
3. 关键业务逻辑必须包含单元测试。
4. 提供 MySQL 建表 SQL。
5. 提供配置文件示例。
6. 提供 README，说明启动方式、模块结构和安全策略。
7. 不需要实现钱包私钥管理、交易签名、提现功能。
8. 避免过度设计，不引入不必要的微服务、MQ 或复杂框架。

### 九、验收标准

- 三种入金类型均能正确识别。
- 假 Token、失败交易、失败 Trace 均不会入账。
- 地址识别准确，不会记到错误用户。
- 同一笔入金只能记账一次。
- 未确认交易不增加可用余额。
- Reorg 不会导致错误入账。
- 程序重启后能够继续扫描。
- 所有核心测试通过。