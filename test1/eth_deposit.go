package test1

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// CheckETHDeposit recognizes only a top-level ETH value transfer.
func CheckETHDeposit(ctx context.Context, client *ethclient.Client, tx *types.Transaction) (*Deposit, error) {
	if tx == nil || tx.To() == nil || tx.Value().Sign() <= 0 {
		return nil, nil
	}
	userID, ok := DepositAddresses[*tx.To()]
	if !ok {
		return nil, nil
	}
	receipt, err := client.TransactionReceipt(ctx, tx.Hash())
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, nil
	}
	return &Deposit{UserID: userID, Asset: "ETH", To: *tx.To(), Amount: new(big.Int).Set(tx.Value()), TxHash: tx.Hash(), Source: "ETH", EventIndex: "top"}, nil
}
