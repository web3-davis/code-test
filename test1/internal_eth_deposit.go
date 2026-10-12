package test1

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type callFrame struct {
	Type  string      `json:"type"`
	From  string      `json:"from"`
	To    string      `json:"to"`
	Value string      `json:"value"`
	Error string      `json:"error"`
	Calls []callFrame `json:"calls"`
}

// CheckInternalETHDeposits traces nested ETH transfers only, excluding the root call.
// Requires a trusted Geth-compatible debug_traceTransaction endpoint.
func CheckInternalETHDeposits(ctx context.Context, client *ethclient.Client, rpcClient *rpc.Client, tx *types.Transaction) ([]Deposit, error) {
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
	var root callFrame
	if err := rpcClient.CallContext(ctx, &root, "debug_traceTransaction", tx.Hash(), map[string]interface{}{
		"tracer": "callTracer", "tracerConfig": map[string]interface{}{"onlyTopCall": false},
	}); err != nil {
		return nil, err
	}
	if root.Type == "" || root.Error != "" {
		return nil, fmt.Errorf("invalid or failed root trace")
	}
	var deposits []Deposit
	var walk func(callFrame, string) error
	walk = func(frame callFrame, path string) error {
		if frame.Error != "" {
			return nil
		} // Failed calls and descendants have no effects.
		if frame.Type == "CALL" || frame.Type == "SELFDESTRUCT" {
			val := frame.Value
			if val == "" {
				val = "0x0"
			}
			amount, ok := new(big.Int).SetString(strings.TrimPrefix(val, "0x"), 16)
			if !ok {
				return fmt.Errorf("invalid trace value at %s: %q", path, val)
			}
			if amount.Sign() > 0 {
				if !common.IsHexAddress(frame.To) {
					return fmt.Errorf("invalid trace recipient at %s: %q", path, frame.To)
				}
				to := common.HexToAddress(frame.To)
				if userID, exists := DepositAddresses[to]; exists {
					deposits = append(deposits, Deposit{UserID: userID, Asset: "ETH", To: to, Amount: amount, TxHash: tx.Hash(), Source: "INTERNAL_ETH", EventIndex: "trace:" + path})
				}
			}
		}
		for i, child := range frame.Calls {
			if err := walk(child, path+"."+strconv.Itoa(i)); err != nil {
				return err
			}
		}
		return nil
	}
	for i, child := range root.Calls {
		if err := walk(child, strconv.Itoa(i)); err != nil {
			return nil, err
		}
	}
	return deposits, nil
}
