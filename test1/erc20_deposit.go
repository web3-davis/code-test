package test1

import (
	"context"
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// CheckERC20Deposits parses successful ERC20 Transfer logs from allowlisted contracts.
func CheckERC20Deposits(ctx context.Context, client *ethclient.Client, tx *types.Transaction) ([]Deposit, error) {
	if tx == nil {
		return nil, nil
	}
	receipt, err := client.TransactionReceipt(ctx, tx.Hash())
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, nil
	}
	var deposits []Deposit
	for _, lg := range receipt.Logs {
		if lg == nil {
			return nil, fmt.Errorf("nil log in receipt")
		}
		token, ok := SupportedTokens[lg.Address]
		if !ok || len(lg.Topics) != 3 || lg.Topics[0] != transferEventID || len(lg.Data) != 32 {
			continue
		}
		to := common.BytesToAddress(lg.Topics[2].Bytes()[12:])
		userID, ok := DepositAddresses[to]
		if !ok {
			continue
		}
		amount := new(big.Int).SetBytes(lg.Data)
		if amount.Sign() <= 0 {
			continue
		}
		deposits = append(deposits, Deposit{UserID: userID, Asset: token, To: to, Amount: amount, TxHash: tx.Hash(), Source: "ERC20", EventIndex: "log:" + strconv.FormatUint(uint64(lg.Index), 10)})
	}
	return deposits, nil
}
