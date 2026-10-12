package main

import (
	"crypto/rand"
	"fmt"
	"log"

	"github.com/gagliardetto/solana-go"
)

func main() {
	// 1. 生成模拟钱包（仅演示用，切勿使用真实私钥）
	senderKey := solana.NewWallet().PrivateKey
	receiverKey := solana.NewWallet().PrivateKey
	sender, receiver := senderKey.PublicKey(), receiverKey.PublicKey()

	// 2. 模拟 Mint 和 RecentBlockhash
	mint := solana.NewWallet().PublicKey()
	var blockhash solana.Hash
	_, _ = rand.Read(blockhash[:])

	// 3. PrepareAccount：计算双方 ATA，并构建接收方 ATA 创建指令（sender 付费）
	senderATA, _, err := PrepareAccount(sender, mint, sender)
	if err != nil {
		log.Fatal(err)
	}
	receiverATA, createIx, err := PrepareAccount(receiver, mint, sender)
	if err != nil {
		log.Fatal(err)
	}
	createData, _ := createIx.Data()
	fmt.Println("== 1. PrepareAccount ==")
	fmt.Println("Sender ATA:  ", senderATA)
	fmt.Println("Receiver ATA:", receiverATA)
	fmt.Printf("Create ATA Ix: program=%s data=%v accounts=%d\n\n", createIx.ProgramID(), createData, len(createIx.Accounts()))

	// 4. BuildMessage：转账 1.5 个 Token（decimals=6 → 1_500_000）
	msg, tx, err := BuildMessage(sender, senderATA, receiverATA, mint, 1_500_000, 6, blockhash)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("== 2. BuildMessage ==")
	fmt.Printf("Message (%d bytes): %x\n\n", len(msg), msg)

	// 5. VerifySignature：签名 + 验签 + 篡改测试
	sig, valid, tamperedValid := VerifySignature(msg, senderKey)
	fmt.Println("== 3. VerifySignature ==")
	fmt.Println("Signature:        ", sig)
	fmt.Println("原始消息验签:      ", valid)
	fmt.Println("篡改消息验签:      ", tamperedValid)
	fmt.Println()

	// 6. 签名填入槽位：签名顺序与 Message 中前 N 个签名账户一一对应，这里只有 sender 一个
	tx.Signatures = []solana.Signature{sig}
	if err := tx.VerifySignatures(); err != nil {
		log.Fatal("交易签名校验失败: ", err)
	}

	// 7. 序列化完整 Transaction 为 Base64
	rawTx, err := tx.ToBase64()
	if err != nil {
		log.Fatal(err)
	}

	// 8. DecodeTransaction
	decoded, err := DecodeTransaction(rawTx)
	if err != nil {
		log.Fatal(err)
	}

	// 9. 打印解析结果
	fmt.Println("== 4. DecodeTransaction ==")
	fmt.Println("RawTx (base64):  ", rawTx)
	fmt.Println("Signatures:      ", decoded.Signatures)
	fmt.Println("FeePayer:        ", decoded.FeePayer)
	fmt.Println("RecentBlockhash: ", decoded.RecentBlockhash)
	fmt.Println("AccountKeys:")
	for i, k := range decoded.AccountKeys {
		fmt.Printf("  [%d] %s\n", i, k)
	}
	for i, ix := range decoded.Instructions {
		fmt.Printf("Instruction[%d]: %s (program=%s)\n", i, ix.Type, ix.ProgramID)
		if ix.Type == "TransferChecked" {
			fmt.Println("  Source:     ", ix.Source)
			fmt.Println("  Destination:", ix.Destination)
			fmt.Println("  Mint:       ", ix.Mint)
			fmt.Println("  Owner:      ", ix.Owner)
			fmt.Println("  Amount:     ", ix.Amount)
			fmt.Println("  Decimals:   ", ix.Decimals)
		}
	}
}
