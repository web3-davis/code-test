package ethereum

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	geth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// BTCTransfer.sol 中用到的 ABI 片段
const contractABIJSON = `[
 {"type":"function","name":"requestBTCTransfer","inputs":[{"name":"btcAddress","type":"string"},{"name":"amount","type":"uint64"}],"outputs":[{"name":"requestId","type":"uint256"}]},
 {"type":"function","name":"startBTCTransfer","inputs":[{"name":"requestId","type":"uint256"}],"outputs":[]},
 {"type":"function","name":"completeBTCTransfer","inputs":[{"name":"requestId","type":"uint256"},{"name":"btcTxHash","type":"bytes32"}],"outputs":[]},
 {"type":"function","name":"failBTCTransfer","inputs":[{"name":"requestId","type":"uint256"},{"name":"reason","type":"string"}],"outputs":[]},
 {"type":"event","name":"BTCTransferRequested","inputs":[{"name":"requestId","type":"uint256","indexed":true},{"name":"requester","type":"address","indexed":true},{"name":"btcAddress","type":"string","indexed":false},{"name":"amount","type":"uint64","indexed":false}]},
 {"type":"event","name":"BTCTransferProcessing","inputs":[{"name":"requestId","type":"uint256","indexed":true}]},
 {"type":"event","name":"BTCTransferCompleted","inputs":[{"name":"requestId","type":"uint256","indexed":true},{"name":"btcTxHash","type":"bytes32","indexed":false},{"name":"amount","type":"uint64","indexed":false},{"name":"btcAddress","type":"string","indexed":false}]},
 {"type":"event","name":"BTCTransferFailed","inputs":[{"name":"requestId","type":"uint256","indexed":true},{"name":"reason","type":"string","indexed":false}]}
]`

var ContractABI, _ = abi.JSON(strings.NewReader(contractABIJSON))

// Backend 是所需的最小链接口，*ethclient.Client 直接满足它。
type Backend interface {
	ChainID(ctx context.Context) (*big.Int, error)
	BlockNumber(ctx context.Context) (uint64, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

// Notifier 以 OPERATOR_ROLE 身份调用合约。
// Demo 中 operator 私钥是本地随机生成的 EVM 测试密钥（与 BTC 无关）；生产应放 KMS/HSM。
type Notifier struct {
	backend  Backend
	contract common.Address
	chainID  *big.Int
	key      *ecdsa.PrivateKey
	nonce    uint64
}

func NewNotifier(backend Backend, contract common.Address, chainID *big.Int, key *ecdsa.PrivateKey) *Notifier {
	return &Notifier{backend: backend, contract: contract, chainID: chainID, key: key}
}

func (n *Notifier) StartTransfer(ctx context.Context, requestID *big.Int) error {
	return n.send(ctx, "startBTCTransfer", requestID)
}

// CompleteTransfer：btcTxHash 为 BTC txid（显示顺序 hex），按原样存为 bytes32。
func (n *Notifier) CompleteTransfer(ctx context.Context, requestID *big.Int, btcTxHash string) error {
	b, err := hex.DecodeString(btcTxHash)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("invalid btc tx hash %q", btcTxHash)
	}
	return n.send(ctx, "completeBTCTransfer", requestID, common.BytesToHash(b))
}

func (n *Notifier) FailTransfer(ctx context.Context, requestID *big.Int, reason string) error {
	return n.send(ctx, "failBTCTransfer", requestID, reason)
}

// send 演示 ABI 编码 + 签名 + 发送，并区分三个阶段：
//  1. 已提交：节点接受了交易（仍可能被丢弃/替换）
//  2. 已上链：拿到 receipt
//  3. 执行成功：receipt.Status == 1（否则合约 revert）
func (n *Notifier) send(ctx context.Context, method string, args ...interface{}) error {
	data, err := ContractABI.Pack(method, args...)
	if err != nil {
		return fmt.Errorf("abi pack %s: %w", method, err)
	}
	// 真实环境：nonce 用 PendingNonceAt，gas 用 EstimateGas / SuggestGasTipCap
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   n.chainID,
		Nonce:     n.nonce,
		GasTipCap: big.NewInt(1e9),
		GasFeeCap: big.NewInt(30e9),
		Gas:       200_000,
		To:        &n.contract,
		Data:      data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(n.chainID), n.key)
	if err != nil {
		return err
	}
	fmt.Printf("  [EVM] %s calldata=0x%s\n", method, hex.EncodeToString(data))

	if err := n.backend.SendTransaction(ctx, signed); err != nil {
		return fmt.Errorf("%s not submitted: %w", method, err)
	}
	n.nonce++
	fmt.Printf("  [EVM] %s submitted, tx=%s\n", method, signed.Hash().Hex())

	var receipt *types.Receipt
	for i := 0; i < 10; i++ { // 真实环境此处应带间隔轮询或使用 bind.WaitMined
		receipt, err = n.backend.TransactionReceipt(ctx, signed.Hash())
		if err == nil {
			break
		}
		if !errors.Is(err, geth.NotFound) {
			return err
		}
	}
	if receipt == nil {
		return fmt.Errorf("%s submitted but not mined yet, tx=%s", method, signed.Hash().Hex())
	}
	fmt.Printf("  [EVM] %s mined in block %d\n", method, receipt.BlockNumber)

	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("%s mined but reverted, tx=%s", method, signed.Hash().Hex())
	}
	fmt.Printf("  [EVM] %s executed successfully\n", method)
	return nil
}

// ---------------- Mock 链：在内存中模拟 BTCTransfer.sol 的状态机 ----------------

const (
	StatusPending uint8 = iota
	StatusProcessing
	StatusCompleted
	StatusFailed
)

type mockRequest struct {
	btcAddress string
	amount     uint64
	status     uint8
	btcTxHash  common.Hash
}

type MockBackend struct {
	chainID  *big.Int
	contract common.Address
	operator common.Address
	block    uint64
	nextID   uint64
	requests map[uint64]*mockRequest
	receipts map[common.Hash]*types.Receipt
}

func NewMockBackend(chainID *big.Int, contract, operator common.Address) *MockBackend {
	return &MockBackend{
		chainID: chainID, contract: contract, operator: operator,
		block: 100, nextID: 1,
		requests: map[uint64]*mockRequest{},
		receipts: map[common.Hash]*types.Receipt{},
	}
}

func (m *MockBackend) ChainID(ctx context.Context) (*big.Int, error) { return m.chainID, nil }

// BlockNumber 每次调用出一个新块，用来模拟确认数增长。
func (m *MockBackend) BlockNumber(ctx context.Context) (uint64, error) {
	m.block++
	return m.block, nil
}

func (m *MockBackend) TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	if r, ok := m.receipts[h]; ok {
		return r, nil
	}
	return nil, geth.NotFound
}

// RequestBTCTransfer 模拟用户调用合约 requestBTCTransfer，返回产生的事件日志。
func (m *MockBackend) RequestBTCTransfer(requester common.Address, btcAddress string, amount uint64) types.Log {
	id := m.nextID
	m.nextID++
	m.requests[id] = &mockRequest{btcAddress: btcAddress, amount: amount, status: StatusPending}

	ev := ContractABI.Events["BTCTransferRequested"]
	data, _ := ev.Inputs.NonIndexed().Pack(btcAddress, amount)
	m.block++
	txHash := crypto.Keccak256Hash([]byte(fmt.Sprintf("request-%d", id)))
	blockHash := crypto.Keccak256Hash([]byte(fmt.Sprintf("block-%d", m.block)))
	m.receipts[txHash] = &types.Receipt{Status: 1, TxHash: txHash, BlockHash: blockHash, BlockNumber: new(big.Int).SetUint64(m.block)}

	fmt.Printf("  [Solidity] emit BTCTransferRequested(id=%d, requester=%s, btcAddress=%s, amount=%d)\n", id, requester.Hex(), btcAddress, amount)
	return types.Log{
		Address:     m.contract,
		Topics:      []common.Hash{ev.ID, common.BigToHash(new(big.Int).SetUint64(id)), common.BytesToHash(requester.Bytes())},
		Data:        data,
		BlockNumber: m.block,
		BlockHash:   blockHash,
		TxHash:      txHash,
		Index:       0,
	}
}

func (m *MockBackend) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	from, err := types.Sender(types.LatestSignerForChainID(m.chainID), tx)
	if err != nil {
		return err
	}
	if tx.To() == nil || *tx.To() != m.contract {
		return errors.New("unknown contract")
	}
	m.block++
	r := &types.Receipt{Status: types.ReceiptStatusSuccessful, TxHash: tx.Hash(), BlockNumber: new(big.Int).SetUint64(m.block)}
	if err := m.execute(from, tx.Data()); err != nil {
		r.Status = types.ReceiptStatusFailed
		fmt.Printf("  [Solidity] revert: %v\n", err)
	}
	m.receipts[tx.Hash()] = r
	return nil
}

// execute 与 BTCTransfer.sol 中的 require 检查保持一致。
func (m *MockBackend) execute(from common.Address, data []byte) error {
	if from != m.operator {
		return errors.New("AccessControl: missing OPERATOR_ROLE")
	}
	method, err := ContractABI.MethodById(data[:4])
	if err != nil {
		return err
	}
	args, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		return err
	}
	id := args[0].(*big.Int).Uint64()
	r, ok := m.requests[id]
	if !ok {
		return errors.New("request not found")
	}
	switch method.Name {
	case "startBTCTransfer":
		if r.status != StatusPending {
			return errors.New("not pending")
		}
		r.status = StatusProcessing
		fmt.Printf("  [Solidity] emit BTCTransferProcessing(id=%d)\n", id)
	case "completeBTCTransfer":
		h := common.Hash(args[1].([32]byte))
		if r.status != StatusProcessing {
			return errors.New("not processing")
		}
		if h == (common.Hash{}) {
			return errors.New("empty btc tx hash")
		}
		r.btcTxHash, r.status = h, StatusCompleted
		fmt.Printf("  [Solidity] emit BTCTransferCompleted(id=%d, btcTxHash=%s, amount=%d, btcAddress=%s)\n", id, h.Hex(), r.amount, r.btcAddress)
	case "failBTCTransfer":
		if r.status != StatusPending && r.status != StatusProcessing {
			return errors.New("invalid status")
		}
		r.status = StatusFailed
		fmt.Printf("  [Solidity] emit BTCTransferFailed(id=%d, reason=%s)\n", id, args[1].(string))
	default:
		return fmt.Errorf("unsupported method %s", method.Name)
	}
	return nil
}

// Status 查询请求状态（对应合约 getRequest）。
func (m *MockBackend) Status(id uint64) uint8 { return m.requests[id].status }
