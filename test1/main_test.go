package test1

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
)

// fakeChain 用内存数据模拟节点的 eth_* / debug_* RPC。
type fakeChain struct {
	blockJSON json.RawMessage
	receipts  map[common.Hash]*types.Receipt
	traces    map[common.Hash]callFrame
}

type fakeEth struct{ c *fakeChain }

func (e *fakeEth) ChainId() *hexutil.Big { return (*hexutil.Big)(big.NewInt(1)) }

func (e *fakeEth) GetBlockByNumber(number string, full bool) json.RawMessage {
	return e.c.blockJSON
}

func (e *fakeEth) GetTransactionReceipt(hash common.Hash) (*types.Receipt, error) {
	r, ok := e.c.receipts[hash]
	if !ok {
		return nil, fmt.Errorf("receipt not found: %s", hash)
	}
	return r, nil
}

type fakeDebug struct{ c *fakeChain }

func (d *fakeDebug) TraceTransaction(hash common.Hash, cfg map[string]interface{}) (callFrame, error) {
	t, ok := d.c.traces[hash]
	if !ok {
		return callFrame{}, fmt.Errorf("trace not found: %s", hash)
	}
	return t, nil
}

func TestScanBlocks(t *testing.T) {
	user1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	user2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	usdt := common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")
	someContract := common.HexToAddress("0x9999999999999999999999999999999999999999")

	key, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(key.PublicKey)
	signer := types.NewEIP155Signer(big.NewInt(1))
	newTx := func(nonce uint64, to common.Address, value int64) *types.Transaction {
		tx, err := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce: nonce, To: &to, Value: big.NewInt(value), Gas: 100000, GasPrice: big.NewInt(1),
		}), signer, key)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}

	txETH := newTx(0, user1, 1000)          // 顶层 ETH 入金
	txERC20 := newTx(1, usdt, 0)            // USDT Transfer 到 user2
	txInternal := newTx(2, someContract, 0) // 合约内部转 ETH 给 user1
	txFailed := newTx(3, user2, 500)        // 失败交易，不应入账

	chain := &fakeChain{receipts: map[common.Hash]*types.Receipt{}, traces: map[common.Hash]callFrame{}}
	receipt := func(tx *types.Transaction, status uint64, logs []*types.Log) {
		chain.receipts[tx.Hash()] = &types.Receipt{
			Status: status, TxHash: tx.Hash(), Logs: append([]*types.Log{}, logs...), BlockNumber: big.NewInt(100), GasUsed: 21000, CumulativeGasUsed: 21000,
		}
	}
	rootTrace := func(tx *types.Transaction, calls ...callFrame) {
		chain.traces[tx.Hash()] = callFrame{Type: "CALL", From: sender.Hex(), To: tx.To().Hex(), Value: hexutil.EncodeBig(tx.Value()), Calls: calls}
	}

	receipt(txETH, types.ReceiptStatusSuccessful, nil)
	rootTrace(txETH)

	receipt(txERC20, types.ReceiptStatusSuccessful, []*types.Log{{
		Address: usdt,
		Topics:  []common.Hash{transferEventID, common.BytesToHash(sender.Bytes()), common.BytesToHash(user2.Bytes())},
		Data:    common.LeftPadBytes(big.NewInt(200).Bytes(), 32),
		TxHash:  txERC20.Hash(),
		Index:   7,
	}})
	rootTrace(txERC20)

	receipt(txInternal, types.ReceiptStatusSuccessful, nil)
	rootTrace(txInternal,
		callFrame{Type: "CALL", From: someContract.Hex(), To: user1.Hex(), Value: "0x5"},
		callFrame{Type: "CALL", From: someContract.Hex(), To: user2.Hex(), Value: "0x9", Error: "execution reverted"}, // 回滚，忽略
	)

	receipt(txFailed, types.ReceiptStatusFailed, nil)

	txs := []*types.Transaction{txETH, txERC20, txInternal, txFailed}
	block := types.NewBlock(&types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(0), GasLimit: 30_000_000},
		&types.Body{Transactions: txs}, nil, trie.NewStackTrie(nil))
	var blockMap map[string]interface{}
	headerJSON, _ := json.Marshal(block.Header())
	_ = json.Unmarshal(headerJSON, &blockMap)
	blockMap["transactions"] = txs
	blockMap["uncles"] = []common.Hash{}
	chain.blockJSON, _ = json.Marshal(blockMap)

	server := rpc.NewServer()
	if err := server.RegisterName("eth", &fakeEth{chain}); err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterName("debug", &fakeDebug{chain}); err != nil {
		t.Fatal(err)
	}
	rpcClient := rpc.DialInProc(server)
	defer rpcClient.Close()
	client := ethclient.NewClient(rpcClient)

	deposits, err := ScanBlocks(context.Background(), client, rpcClient, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range deposits {
		t.Logf("%+v", d)
	}

	want := []Deposit{
		{UserID: "user_001", Asset: "ETH", To: user1, Amount: big.NewInt(1000), TxHash: txETH.Hash(), Source: "ETH", EventIndex: "top"},
		{UserID: "user_002", Asset: "USDT", To: user2, Amount: big.NewInt(200), TxHash: txERC20.Hash(), Source: "ERC20", EventIndex: "log:7"},
		{UserID: "user_001", Asset: "ETH", To: user1, Amount: big.NewInt(5), TxHash: txInternal.Hash(), Source: "INTERNAL_ETH", EventIndex: "trace:0"},
	}
	if len(deposits) != len(want) {
		t.Fatalf("got %d deposits, want %d", len(deposits), len(want))
	}
	for i, w := range want {
		g := deposits[i]
		if g.UserID != w.UserID || g.Asset != w.Asset || g.To != w.To || g.Amount.Cmp(w.Amount) != 0 ||
			g.TxHash != w.TxHash || g.Source != w.Source || g.EventIndex != w.EventIndex {
			t.Errorf("deposit[%d] = %+v, want %+v", i, g, w)
		}
	}
}
