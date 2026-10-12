package listener

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	geth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"btctransfer/internal/bitcoin"
	"btctransfer/internal/ethereum"
	"btctransfer/internal/mpc"
)

// pollInterval 轮询区块高度的间隔（Demo 用较小值，真实环境约等于出块间隔）
var pollInterval = 10 * time.Millisecond

type Listener struct {
	Backend       ethereum.Backend
	Contract      common.Address
	ChainID       *big.Int
	Confirmations uint64
	Notifier      *ethereum.Notifier
	Signer        mpc.Signer

	// 演示用内存去重：只能演示幂等，服务重启即丢失。
	// 生产必须持久化任务状态（requestId -> 状态/txid），重启后先对账再继续。
	processed map[string]bool
}

// SubscribeRequested 真实订阅示例（WebSocket）：
//
//	client, _ := ethclient.Dial("wss://...")
//	logs, sub, _ := SubscribeRequested(ctx, client, contract)
//	go l.ListenBTCTransferEvents(ctx, logs)
//	// 还需处理 <-sub.Err() 断线重连，并从上次处理的区块用 FilterLogs 补扫
func SubscribeRequested(ctx context.Context, client *ethclient.Client, contract common.Address) (<-chan types.Log, geth.Subscription, error) {
	q := geth.FilterQuery{
		Addresses: []common.Address{contract},
		Topics:    [][]common.Hash{{ethereum.ContractABI.Events["BTCTransferRequested"].ID}},
	}
	ch := make(chan types.Log, 64)
	sub, err := client.SubscribeFilterLogs(ctx, q, ch)
	return ch, sub, err
}

// ListenBTCTransferEvents 消费 BTCTransferRequested 日志（真实订阅或模拟事件均可）。
func (l *Listener) ListenBTCTransferEvents(ctx context.Context, logs <-chan types.Log) error {
	if l.processed == nil {
		l.processed = map[string]bool{}
	}
	// 校验连接的链是否是预期的 ChainID，防止连错网络
	chainID, err := l.Backend.ChainID(ctx)
	if err != nil {
		return err
	}
	if chainID.Cmp(l.ChainID) != 0 {
		return fmt.Errorf("chainID mismatch: got %s want %s", chainID, l.ChainID)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case lg, ok := <-logs:
			if !ok {
				return nil
			}
			l.handleLog(ctx, lg)
		}
	}
}

func (l *Listener) handleLog(ctx context.Context, lg types.Log) {
	// 1. 校验事件来源合约和事件类型
	ev := ethereum.ContractABI.Events["BTCTransferRequested"]
	if lg.Address != l.Contract || len(lg.Topics) != 3 || lg.Topics[0] != ev.ID {
		fmt.Printf("  [Listener] ignore log from %s\n", lg.Address.Hex())
		return
	}

	// 2. 解析事件
	requestID := new(big.Int).SetBytes(lg.Topics[1].Bytes())
	requester := common.BytesToAddress(lg.Topics[2].Bytes())
	vals, err := ev.Inputs.NonIndexed().Unpack(lg.Data)
	if err != nil {
		fmt.Printf("  [Listener] decode error: %v\n", err)
		return
	}
	btcAddress, amount := vals[0].(string), vals[1].(uint64)
	key := requestID.String()
	fmt.Printf("  [Listener] parsed: requestId=%s requester=%s btcAddress=%s amount=%d block=%d tx=%s logIndex=%d\n",
		key, requester.Hex(), btcAddress, amount, lg.BlockNumber, lg.TxHash.Hex(), lg.Index)

	// 3. Reorg：日志被移除。因为我们只在足够确认后才处理，这里正常只需忽略；
	//    若已经开始处理，说明确认数配置不足，需要人工介入（不能自动“撤回”BTC）。
	if lg.Removed {
		if l.processed[key] {
			fmt.Printf("  [Listener] ALERT: removed log for already-processed request %s\n", key)
		} else {
			fmt.Printf("  [Listener] removed log (reorg) for request %s, skip\n", key)
		}
		return
	}

	// 4. 幂等：同一 requestId 只触发一次 BTC 转账
	if l.processed[key] {
		fmt.Printf("  [Listener] duplicate request %s, skip\n", key)
		return
	}

	// 5. 等待 EVM 确认，并确认日志仍在主链上
	if err := l.waitConfirmed(ctx, lg); err != nil {
		fmt.Printf("  [Listener] request %s not confirmed: %v\n", key, err)
		return
	}

	// 6. 业务校验失败（确定未发送 BTC）-> 直接标记 Failed
	//    注：Demo 假设请求已通过业务授权；真实系统还需校验用户 BTC 余额、额度、资金归属。
	if _, err := bitcoin.ValidateAddress(btcAddress); err != nil {
		l.processed[key] = true
		l.fail(ctx, requestID, err.Error())
		return
	}

	// 7. Pending -> Processing
	if err := retry(3, func() error { return l.Notifier.StartTransfer(ctx, requestID) }); err != nil {
		fmt.Printf("  [Listener] startBTCTransfer failed, BTC not sent: %v\n", err)
		return
	}
	// Pending -> Processing 成功后才标记已处理；重复发起由合约 require(status == Pending) 兜底
	l.processed[key] = true

	// 8. 构建、签名、广播 BTC 交易
	txid, err := bitcoin.BitcoinTransfer(ctx, btcAddress, int64(amount), l.Signer)
	if err != nil {
		if errors.Is(err, bitcoin.ErrBroadcastUnknown) {
			// 可能已广播：保持 Processing，等待对账，绝不重新构建交易
			fmt.Printf("  [Listener] ALERT: request %s txid=%s state unknown, keep Processing: %v\n", key, txid, err)
			return
		}
		// 广播之前的确定性错误（地址/余额/签名失败等），BTC 一定未发出
		l.fail(ctx, requestID, err.Error())
		return
	}

	// 9. 等待 BTC 确认（广播成功 != 确认成功）
	if err := bitcoin.WaitForConfirmation(ctx, txid); err != nil {
		fmt.Printf("  [Listener] ALERT: request %s tx %s not confirmed: %v\n", key, txid, err)
		return
	}

	// 10. 通知合约完成。失败只重试通知，绝不重新发送 BTC。
	if err := retry(3, func() error { return l.Notifier.CompleteTransfer(ctx, requestID, txid) }); err != nil {
		fmt.Printf("  [Listener] ALERT: BTC sent (txid=%s) but completeBTCTransfer failed, need re-notify: %v\n", txid, err)
	}
}

func (l *Listener) waitConfirmed(ctx context.Context, lg types.Log) error {
	for {
		head, err := l.Backend.BlockNumber(ctx)
		if err != nil {
			return err
		}
		if head >= lg.BlockNumber+l.Confirmations {
			fmt.Printf("  [Listener] log block %d, head %d, confirmations >= %d\n", lg.BlockNumber, head, l.Confirmations)
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	// 确认数够了再查一次 receipt，确保日志所在区块仍是主链区块
	r, err := l.Backend.TransactionReceipt(ctx, lg.TxHash)
	if err != nil {
		return err
	}
	if r.BlockHash != lg.BlockHash || r.Status != types.ReceiptStatusSuccessful {
		return errors.New("log reorged out")
	}
	return nil
}

func (l *Listener) fail(ctx context.Context, requestID *big.Int, reason string) {
	fmt.Printf("  [Listener] request %s failed: %s\n", requestID, reason)
	if err := retry(3, func() error { return l.Notifier.FailTransfer(ctx, requestID, reason) }); err != nil {
		fmt.Printf("  [Listener] ALERT: failBTCTransfer notify failed: %v\n", err)
	}
}

// retry 仅用于重试 EVM 通知类调用（合约状态机保证重复调用会 revert，不会重复生效）。
func retry(n int, fn func() error) error {
	var err error
	for i := 0; i < n; i++ {
		if err = fn(); err == nil {
			return nil
		}
		fmt.Printf("  [Listener] attempt %d failed: %v\n", i+1, err)
	}
	return err
}
