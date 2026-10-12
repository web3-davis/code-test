package test1

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// Demo configuration only. Treat these maps as immutable while scanning.
var DepositAddresses = map[common.Address]string{
	common.HexToAddress("0x1111111111111111111111111111111111111111"): "user_001",
	common.HexToAddress("0x2222222222222222222222222222222222222222"): "user_002",
}

// Ethereum mainnet ERC20 allowlist. The caller must verify the RPC chain ID.
var SupportedTokens = map[common.Address]string{
	common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"): "USDT",
	common.HexToAddress("0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"): "USDC",
}

var transferEventID = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

type Deposit struct {
	UserID     string
	Asset      string
	To         common.Address
	Amount     *big.Int // Wei for ETH, raw smallest units for ERC20
	TxHash     common.Hash
	Source     string // ETH, ERC20, INTERNAL_ETH
	EventIndex string // top, log:<index>, trace:<path>
}

// ScanBlocks scans blocks in [from, to] and runs all three deposit checks on each transaction.
func ScanBlocks(ctx context.Context, client *ethclient.Client, rpcClient *rpc.Client, from, to uint64) ([]Deposit, error) {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, err
	}
	if chainID.Cmp(big.NewInt(1)) != 0 {
		return nil, fmt.Errorf("unexpected chain id %s, want 1", chainID)
	}
	var deposits []Deposit
	for n := from; n <= to; n++ {
		block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(n))
		if err != nil {
			return nil, fmt.Errorf("get block %d: %w", n, err)
		}
		for _, tx := range block.Transactions() {
			d, err := CheckETHDeposit(ctx, client, tx)
			if err != nil {
				return nil, fmt.Errorf("check eth tx %s: %w", tx.Hash(), err)
			}
			if d != nil {
				deposits = append(deposits, *d)
			}
			ds, err := CheckERC20Deposits(ctx, client, tx)
			if err != nil {
				return nil, fmt.Errorf("check erc20 tx %s: %w", tx.Hash(), err)
			}
			deposits = append(deposits, ds...)
			ds, err = CheckInternalETHDeposits(ctx, client, rpcClient, tx)
			if err != nil {
				return nil, fmt.Errorf("check internal eth tx %s: %w", tx.Hash(), err)
			}
			deposits = append(deposits, ds...)
		}
	}
	return deposits, nil
}
