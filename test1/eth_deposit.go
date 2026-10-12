package test1

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
)

// CheckETHDeposit recognizes only a top-level ETH value transfer.
func CheckETHDeposit(tx *types.Transaction, receipt *types.Receipt) (*Deposit, error) {
	if err := checkReceipt(tx, receipt); err != nil {
		return nil, err
	}
	if tx.To() == nil || tx.Value().Sign() <= 0 || receipt.Status != types.ReceiptStatusSuccessful {
		return nil, nil
	}
	userID, ok := DepositAddresses[*tx.To()]
	if !ok {
		return nil, nil
	}
	return &Deposit{UserID: userID, Asset: "ETH", To: *tx.To(), Amount: new(big.Int).Set(tx.Value()), TxHash: tx.Hash(), Source: "ETH", EventIndex: "top"}, nil
}
