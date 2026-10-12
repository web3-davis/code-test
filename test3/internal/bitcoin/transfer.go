package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"btctransfer/internal/mpc"
)

// 演示配置（真实环境从配置中心读取）
var (
	Network        = &chaincfg.TestNet3Params
	KeyID          = "btc-hot-wallet-01" // MPC 密钥标识
	FeeRate  int64 = 10                  // sat/vB
	MaxFee   int64 = 50_000              // 签名前手续费上限保护
	MinConfs int64 = 6
)

const DustLimit int64 = 546 // 保守取 P2PKH 的 dust 阈值

// ErrBroadcastUnknown：交易可能已经广播出去但结果未知。
// 调用方必须保持 Processing 并人工/自动对账，绝不能标记 Failed 后重新构建交易。
var ErrBroadcastUnknown = errors.New("btc broadcast result unknown")

type UTXO struct {
	TxHash   chainhash.Hash
	Index    uint32
	Value    int64
	PkScript []byte
}

// ValidateAddress 校验地址格式及网络类型。
func ValidateAddress(btcAddress string) (btcutil.Address, error) {
	addr, err := btcutil.DecodeAddress(btcAddress, Network)
	if err != nil {
		return nil, fmt.Errorf("invalid btc address: %w", err)
	}
	if !addr.IsForNet(Network) {
		return nil, fmt.Errorf("btc address %s is not for %s", btcAddress, Network.Name)
	}
	return addr, nil
}

// BitcoinTransfer 构建 P2WPKH 交易 -> MPC 签名 -> 广播，返回 txid。
// 注意：返回成功只代表“已广播”，不代表已确认。
func BitcoinTransfer(ctx context.Context, btcAddress string, amount int64, signer mpc.Signer) (string, error) {
	// 1. 参数校验
	toAddr, err := ValidateAddress(btcAddress)
	if err != nil {
		return "", err
	}
	if amount <= 0 {
		return "", fmt.Errorf("amount must be > 0")
	}
	if amount < DustLimit {
		return "", fmt.Errorf("amount %d below dust limit %d", amount, DustLimit)
	}

	// 热钱包地址由 MPC 公钥推导（P2WPKH），同时作为找零地址
	pubBytes, err := signer.PublicKey(ctx, KeyID)
	if err != nil {
		return "", err
	}
	pubKey, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return "", err
	}
	fromAddr, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(pubBytes), Network)
	if err != nil {
		return "", err
	}
	fromScript, _ := txscript.PayToAddrScript(fromAddr)
	toScript, _ := txscript.PayToAddrScript(toAddr)

	// 2. 选择 UTXO（简单累加，直到覆盖 金额 + 手续费）
	utxos := fetchUTXOs(fromScript)
	var selected []UTXO
	var total, fee int64
	for _, u := range utxos {
		selected = append(selected, u)
		total += u.Value
		fee = estimateVSize(len(selected), 2) * FeeRate
		if total >= amount+fee {
			break
		}
	}
	if total < amount+fee {
		return "", fmt.Errorf("insufficient funds: have %d, need %d", total, amount+fee)
	}

	// 3. 构建交易
	tx := wire.NewMsgTx(2)
	prevOuts := txscript.NewMultiPrevOutFetcher(nil)
	for _, u := range selected {
		op := wire.NewOutPoint(&u.TxHash, u.Index)
		tx.AddTxIn(wire.NewTxIn(op, nil, nil))
		prevOuts.AddPrevOut(*op, wire.NewTxOut(u.Value, u.PkScript))
	}
	tx.AddTxOut(wire.NewTxOut(amount, toScript))
	if change := total - amount - fee; change >= DustLimit {
		tx.AddTxOut(wire.NewTxOut(change, fromScript))
	} else {
		fee = total - amount // 找零太小直接并入手续费
	}

	// 签名前审核交易内容：收款地址、金额、找零地址、手续费
	if err := checkTx(tx, toScript, amount, fromScript, total, fee); err != nil {
		return "", fmt.Errorf("pre-sign check failed: %w", err)
	}

	// 4. 逐个输入计算 BIP143 摘要 -> MPC 签名 -> 验签 -> 填充 Witness
	sigHashes := txscript.NewTxSigHashes(tx, prevOuts)
	for i, u := range selected {
		digest, err := txscript.CalcWitnessSigHash(u.PkScript, sigHashes, txscript.SigHashAll, tx, i, u.Value)
		if err != nil {
			return "", err
		}
		der, err := signer.Sign(ctx, KeyID, digest)
		if err != nil {
			return "", fmt.Errorf("mpc sign input %d: %w", i, err)
		}
		sig, err := ecdsa.ParseDERSignature(der)
		if err != nil {
			return "", fmt.Errorf("mpc returned non-DER signature: %w", err)
		}
		if !sig.Verify(digest, pubKey) {
			return "", fmt.Errorf("mpc signature verify failed for input %d", i)
		}
		// P2WPKH witness: <DER || SIGHASH_ALL> <compressed pubkey>
		tx.TxIn[i].Witness = wire.TxWitness{append(der, byte(txscript.SigHashAll)), pubBytes}
	}

	// 用脚本引擎做一次完整校验，确保交易真的能被节点接受
	for i, u := range selected {
		vm, err := txscript.NewEngine(u.PkScript, tx, i, txscript.StandardVerifyFlags, nil, sigHashes, u.Value, prevOuts)
		if err != nil {
			return "", err
		}
		if err := vm.Execute(); err != nil {
			return "", fmt.Errorf("script verify input %d: %w", i, err)
		}
	}

	// 5. 序列化并广播
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", err
	}
	txid := tx.TxHash().String()
	fmt.Printf("  [BTC] from=%s to=%s amount=%d fee=%d inputs=%d outputs=%d\n",
		fromAddr, toAddr, amount, fee, len(tx.TxIn), len(tx.TxOut))
	fmt.Printf("  [BTC] raw tx: %s\n", hex.EncodeToString(buf.Bytes()))
	if err := broadcast(buf.Bytes(), txid); err != nil {
		// 无法判断节点是否收到：不能当作失败重试
		return txid, fmt.Errorf("%w: %v", ErrBroadcastUnknown, err)
	}
	return txid, nil
}

// estimateVSize：P2WPKH 近似 vsize = 11 + 68*in + 31*out
func estimateVSize(nIn, nOut int) int64 {
	return int64(11 + 68*nIn + 31*nOut)
}

func checkTx(tx *wire.MsgTx, toScript []byte, amount int64, changeScript []byte, inTotal, fee int64) error {
	if len(tx.TxOut) == 0 || !bytes.Equal(tx.TxOut[0].PkScript, toScript) || tx.TxOut[0].Value != amount {
		return errors.New("recipient output mismatch")
	}
	var outTotal int64
	for i, o := range tx.TxOut {
		outTotal += o.Value
		if i > 0 && !bytes.Equal(o.PkScript, changeScript) {
			return errors.New("change output must go back to hot wallet")
		}
	}
	if inTotal-outTotal != fee || fee <= 0 || fee > MaxFee {
		return fmt.Errorf("unexpected fee %d", inTotal-outTotal)
	}
	return nil
}

// fetchUTXOs Mock：真实环境从 Bitcoin 节点 / 索引服务查询热钱包的已确认 UTXO。
func fetchUTXOs(pkScript []byte) []UTXO {
	h1, _ := chainhash.NewHashFromStr("a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90")
	h2, _ := chainhash.NewHashFromStr("0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0")
	return []UTXO{
		{TxHash: *h1, Index: 0, Value: 30_000, PkScript: pkScript},
		{TxHash: *h2, Index: 1, Value: 80_000, PkScript: pkScript},
	}
}

// broadcast Mock：真实环境调用 bitcoind sendrawtransaction。
func broadcast(raw []byte, txid string) error {
	fmt.Printf("  [BTC] mock broadcast ok, txid=%s\n", txid)
	return nil
}

// WaitForConfirmation Mock：真实环境轮询 getrawtransaction/gettxout 直到确认数 >= MinConfs，
// 同时处理交易被替换、长时间未确认（需人工介入/CPFP）等情况。
func WaitForConfirmation(ctx context.Context, txid string) error {
	fmt.Printf("  [BTC] mock: tx %s reached %d confirmations\n", txid, MinConfs)
	return nil
}
