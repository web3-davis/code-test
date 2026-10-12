package listener

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"btctransfer/internal/bitcoin"
	"btctransfer/internal/ethereum"
	"btctransfer/internal/mpc"
)

const testnetAddr = "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx"

// countingSigner 统计发起 BTC 转账的次数（每笔转账调用一次 PublicKey）。
type countingSigner struct {
	mpc.Signer
	transfers int
}

func (s *countingSigner) PublicKey(ctx context.Context, keyID string) ([]byte, error) {
	s.transfers++
	return s.Signer.PublicKey(ctx, keyID)
}

func setup(t *testing.T) (*ethereum.MockBackend, *Listener, *countingSigner, *ecdsa.PrivateKey) {
	t.Helper()
	chainID := big.NewInt(11155111)
	contract := common.HexToAddress("0x00000000000000000000000000000000B7C0FFEE")
	operatorKey, _ := crypto.GenerateKey()
	chain := ethereum.NewMockBackend(chainID, contract, crypto.PubkeyToAddress(operatorKey.PublicKey))
	signer := &countingSigner{Signer: mpc.NewMockMPCSigner(bitcoin.KeyID)}
	l := &Listener{
		Backend:       chain,
		Contract:      contract,
		ChainID:       chainID,
		Confirmations: 3,
		Notifier:      ethereum.NewNotifier(chain, contract, chainID, operatorKey),
		Signer:        signer,
	}
	return chain, l, signer, operatorKey
}

func run(t *testing.T, l *Listener, logs ...types.Log) {
	t.Helper()
	ch := make(chan types.Log, len(logs))
	for _, lg := range logs {
		ch <- lg
	}
	close(ch)
	if err := l.ListenBTCTransferEvents(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
}

func TestListenerScenarios(t *testing.T) {
	chain, l, signer, _ := setup(t)
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")

	ok := chain.RequestBTCTransfer(user, testnetAddr, 50_000)
	mainnet := chain.RequestBTCTransfer(user, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", 20_000)
	removed := chain.RequestBTCTransfer(user, testnetAddr, 10_000)
	removed.Removed = true
	other := chain.RequestBTCTransfer(user, testnetAddr, 10_000)
	other.Address = common.HexToAddress("0xdead") // 非目标合约的日志

	run(t, l, ok, ok, mainnet, removed, other)

	want := map[uint64]uint8{
		1: ethereum.StatusCompleted, // 正常转账
		2: ethereum.StatusFailed,    // 测试网请求使用主网地址
		3: ethereum.StatusPending,   // Reorg 移除的日志被忽略
		4: ethereum.StatusPending,   // 伪造合约地址的日志被忽略
	}
	for id, s := range want {
		if got := chain.Status(id); got != s {
			t.Errorf("request %d status = %d, want %d", id, got, s)
		}
	}
	if signer.transfers != 1 {
		t.Errorf("BTC transfers = %d, want 1 (duplicate event must not resend)", signer.transfers)
	}
}

// startBTCTransfer 失败时 BTC 未发出，重新投递事件后应能继续处理。
func TestRetryAfterStartFailure(t *testing.T) {
	chain, l, signer, operatorKey := setup(t)
	lg := chain.RequestBTCTransfer(common.HexToAddress("0x1111"), testnetAddr, 50_000)

	// 用没有 OPERATOR_ROLE 的密钥发送，合约调用会 revert
	badKey, _ := crypto.GenerateKey()
	l.Notifier = ethereum.NewNotifier(chain, l.Contract, l.ChainID, badKey)
	run(t, l, lg)
	if chain.Status(1) != ethereum.StatusPending || signer.transfers != 0 {
		t.Fatalf("after failed start: status=%d transfers=%d, want Pending/0", chain.Status(1), signer.transfers)
	}

	l.Notifier = ethereum.NewNotifier(chain, l.Contract, l.ChainID, operatorKey)
	run(t, l, lg)
	if chain.Status(1) != ethereum.StatusCompleted || signer.transfers != 1 {
		t.Fatalf("after retry: status=%d transfers=%d, want Completed/1", chain.Status(1), signer.transfers)
	}
}

func TestContextCancel(t *testing.T) {
	chain, l, _, _ := setup(t)
	l.Confirmations = 1 << 40 // 永远达不到确认数
	lg := chain.RequestBTCTransfer(common.HexToAddress("0x1111"), testnetAddr, 50_000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.waitConfirmed(ctx, lg); err == nil {
		t.Fatal("want ctx error")
	}
}
