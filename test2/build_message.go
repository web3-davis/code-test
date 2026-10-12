package main

import (
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/token"
)

// BuildMessage 构建 SPL Token TransferChecked 转账交易，返回序列化后的 Message 和未签名的 Transaction。
func BuildMessage(
	sender solana.PublicKey,
	senderTokenAccount solana.PublicKey,
	receiverTokenAccount solana.PublicKey,
	mint solana.PublicKey,
	amount uint64,
	decimals uint8,
	recentBlockhash solana.Hash,
) ([]byte, *solana.Transaction, error) {
	// 1. TransferChecked 指令：链上会校验 mint 和 decimals，比普通 Transfer 更安全
	ix, err := token.NewTransferCheckedInstruction(
		amount,
		decimals,
		senderTokenAccount,   // source
		mint,                 // mint
		receiverTokenAccount, // destination
		sender,               // owner（授权签名者），假设 sender 就是 Token Account 的 owner
		nil,                  // 非多签
	).ValidateAndBuild()
	if err != nil {
		return nil, nil, err
	}

	// 2. 用 recentBlockhash 构建 Transaction，sender 同时作为 Fee Payer
	tx, err := solana.NewTransaction(
		[]solana.Instruction{ix},
		recentBlockhash,
		solana.TransactionPayer(sender),
	)
	if err != nil {
		return nil, nil, err
	}

	// 3. 序列化 Message —— 这就是签名的对象
	msg, err := tx.Message.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	return msg, tx, nil
}
