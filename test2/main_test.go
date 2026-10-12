package main

import (
	"encoding/hex"
	"testing"

	"github.com/gagliardetto/solana-go"
)

// 构建 -> 签名 -> 序列化 -> 解析，解析结果应与构建时一致。
func TestRoundTrip(t *testing.T) {
	senderKey := solana.NewWallet().PrivateKey
	sender := senderKey.PublicKey()
	receiver := solana.NewWallet().PublicKey()
	mint := solana.NewWallet().PublicKey()
	blockhash := solana.Hash{1, 2, 3}

	senderATA, _, err := PrepareAccount(sender, mint, sender)
	if err != nil {
		t.Fatal(err)
	}
	receiverATA, createIx, err := PrepareAccount(receiver, mint, sender)
	if err != nil {
		t.Fatal(err)
	}

	// ATA 与 SDK 计算结果一致，指令为 CreateIdempotent
	wantATA, _, _ := solana.FindAssociatedTokenAddress(receiver, mint)
	if receiverATA != wantATA {
		t.Fatalf("ATA = %s, want %s", receiverATA, wantATA)
	}
	if data, _ := createIx.Data(); len(data) != 1 || data[0] != 1 {
		t.Fatalf("create ATA data = %v, want [1]", data)
	}

	msg, tx, err := BuildMessage(sender, senderATA, receiverATA, mint, 1_500_000, 6, blockhash)
	if err != nil {
		t.Fatal(err)
	}

	sig, valid, tamperedValid := VerifySignature(msg, senderKey)
	if !valid || tamperedValid {
		t.Fatalf("valid=%v tamperedValid=%v, want true/false", valid, tamperedValid)
	}

	tx.Signatures = []solana.Signature{sig}
	if err := tx.VerifySignatures(); err != nil {
		t.Fatal(err)
	}

	// Base64 和 Hex 两种格式都能解析
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	b64, _ := tx.ToBase64()
	for _, rawTx := range []string{b64, hex.EncodeToString(raw)} {
		d, err := DecodeTransaction(rawTx)
		if err != nil {
			t.Fatal(err)
		}
		if d.FeePayer != sender || d.RecentBlockhash != blockhash || len(d.Signatures) != 1 || d.Signatures[0] != sig {
			t.Fatalf("header mismatch: %+v", d)
		}
		if len(d.Instructions) != 1 {
			t.Fatalf("got %d instructions, want 1", len(d.Instructions))
		}
		ix := d.Instructions[0]
		if ix.Type != "TransferChecked" || ix.Source != senderATA || ix.Destination != receiverATA ||
			ix.Mint != mint || ix.Owner != sender || ix.Amount != 1_500_000 || ix.Decimals != 6 {
			t.Fatalf("instruction mismatch: %+v", ix)
		}
	}
}

// 格式错误的输入应返回 error，而不是 panic。
func TestDecodeInvalid(t *testing.T) {
	// 1 个签名 + Header(1,0,0) + 1 个账户 + Blockhash + 1 条指令（programIdIndex=5 越界）
	b := append([]byte{1}, make([]byte, 64)...)
	b = append(b, 1, 0, 0, 1)
	b = append(b, make([]byte, 64)...)
	b = append(b, 1, 5, 0, 0)

	for name, rawTx := range map[string]string{
		"非 hex/base64": "not-a-tx!",
		"截断的数据":        "AQID",
		"程序索引越界":       hex.EncodeToString(b),
	} {
		if _, err := DecodeTransaction(rawTx); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
