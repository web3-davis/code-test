package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
)

// DecodedInstruction 单条指令的解析结果
type DecodedInstruction struct {
	ProgramID solana.PublicKey
	Type      string
	// 以下字段仅 TransferChecked 有值
	Source      solana.PublicKey
	Mint        solana.PublicKey
	Destination solana.PublicKey
	Owner       solana.PublicKey
	Amount      uint64
	Decimals    uint8
}

// DecodedTransaction 交易的解析结果
type DecodedTransaction struct {
	Signatures      []solana.Signature
	FeePayer        solana.PublicKey
	RecentBlockhash solana.Hash
	AccountKeys     []solana.PublicKey
	Instructions    []DecodedInstruction
}

// DecodeTransaction 反解析 Raw Transaction（Hex 或 Base64），只处理 Legacy Transaction。
// Raw Transaction = 签名列表 + Message；Message = Header + AccountKeys + RecentBlockhash + Instructions。
func DecodeTransaction(rawTx string) (*DecodedTransaction, error) {
	// 1. Hex 或 Base64 解码
	data, err := hex.DecodeString(rawTx)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(rawTx)
		if err != nil {
			return nil, fmt.Errorf("rawTx 既不是 hex 也不是 base64: %w", err)
		}
	}

	// 2. 反序列化完整 Transaction
	tx, err := solana.TransactionFromBytes(data)
	if err != nil {
		return nil, err
	}

	if len(tx.Message.AccountKeys) == 0 {
		return nil, fmt.Errorf("交易没有账户")
	}

	// 3~6. 签名、Fee Payer（AccountKeys[0]）、Blockhash、AccountKeys
	result := &DecodedTransaction{
		Signatures:      tx.Signatures,
		FeePayer:        tx.Message.AccountKeys[0],
		RecentBlockhash: tx.Message.RecentBlockhash,
		AccountKeys:     tx.Message.AccountKeys,
	}

	// 7. 解析指令：指令里存的是账户索引，需要映射回 AccountKeys
	for i, ci := range tx.Message.Instructions {
		if int(ci.ProgramIDIndex) >= len(tx.Message.AccountKeys) {
			return nil, fmt.Errorf("指令 %d 的 programIdIndex %d 越界", i, ci.ProgramIDIndex)
		}
		programID := tx.Message.AccountKeys[ci.ProgramIDIndex]
		item := DecodedInstruction{ProgramID: programID, Type: "Unknown"}

		// 8. 识别 SPL Token TransferChecked 指令
		if programID.Equals(solana.TokenProgramID) {
			accounts, err := ci.ResolveInstructionAccounts(&tx.Message)
			if err != nil {
				return nil, err
			}
			decoded, err := token.DecodeInstruction(accounts, ci.Data)
			if err != nil {
				return nil, err
			}
			// 9. 还原 Token Account、Mint、Amount、Decimals
			if tc, ok := decoded.Impl.(*token.TransferChecked); ok {
				item.Type = "TransferChecked"
				item.Source = tc.GetSourceAccount().PublicKey
				item.Mint = tc.GetMintAccount().PublicKey
				item.Destination = tc.GetDestinationAccount().PublicKey
				item.Owner = tc.GetOwnerAccount().PublicKey
				item.Amount = *tc.Amount
				item.Decimals = *tc.Decimals
			}
		}
		result.Instructions = append(result.Instructions, item)
	}

	// 10. 返回结构化结果
	return result, nil
}
