package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"btctransfer/internal/bitcoin"
	"btctransfer/internal/ethereum"
	"btctransfer/internal/listener"
	"btctransfer/internal/mpc"
)

func main() {
	ctx := context.Background()
	chainID := big.NewInt(11155111) // Sepolia
	contract := common.HexToAddress("0x00000000000000000000000000000000B7C0FFEE")
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")

	// operator：EVM 测试密钥，仅用于给合约调用签名（对应合约 OPERATOR_ROLE）
	operatorKey, _ := crypto.GenerateKey()
	chain := ethereum.NewMockBackend(chainID, contract, crypto.PubkeyToAddress(operatorKey.PublicKey))

	l := &listener.Listener{
		Backend:       chain,
		Contract:      contract,
		ChainID:       chainID,
		Confirmations: 3,
		Notifier:      ethereum.NewNotifier(chain, contract, chainID, operatorKey),
		Signer:        mpc.NewMockMPCSigner(bitcoin.KeyID),
	}

	logs := make(chan types.Log, 10)

	fmt.Println("== 1. 用户调用 requestBTCTransfer（正常请求）")
	ok := chain.RequestBTCTransfer(user, "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", 50_000)
	logs <- ok

	fmt.Println("== 2. 同一事件被重复投递（幂等）")
	logs <- ok

	fmt.Println("== 3. 主网地址出现在测试网请求中（业务错误 -> Failed）")
	logs <- chain.RequestBTCTransfer(user, "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", 20_000)

	fmt.Println("== 4. Reorg 移除的日志")
	removed := chain.RequestBTCTransfer(user, "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx", 10_000)
	removed.Removed = true
	logs <- removed
	close(logs)

	fmt.Println("\n== Listener 开始处理")
	if err := l.ListenBTCTransferEvents(ctx, logs); err != nil {
		fmt.Println("listener error:", err)
	}

	names := []string{"Pending", "Processing", "Completed", "Failed"}
	fmt.Println("\n== 最终合约状态")
	for id := uint64(1); id <= 3; id++ {
		fmt.Printf("  request %d: %s\n", id, names[chain.Status(id)])
	}
}
