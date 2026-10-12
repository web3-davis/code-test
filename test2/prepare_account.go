package main

import (
	"github.com/gagliardetto/solana-go"
)

// PrepareAccount 计算 owner 在 mint 下的 ATA 地址，并构建 CreateIdempotent 指令。
// 只构建指令，不查询链上状态，也不发送交易。
func PrepareAccount(owner, mint, payer solana.PublicKey) (solana.PublicKey, solana.Instruction, error) {
	// 1. ATA 是 PDA：seeds = [owner, TokenProgramID, mint]，由 Associated Token Program 派生，没有私钥
	ata, _, err := solana.FindProgramAddress(
		[][]byte{owner.Bytes(), solana.TokenProgramID.Bytes(), mint.Bytes()},
		solana.SPLAssociatedTokenAccountProgramID,
	)
	if err != nil {
		return solana.PublicKey{}, nil, err
	}

	// 2. 构建 Create Associated Token Account 指令的账户列表（顺序由 ATA 程序规定）
	accounts := solana.AccountMetaSlice{
		solana.Meta(payer).WRITE().SIGNER(), // 支付租金
		solana.Meta(ata).WRITE(),            // 待创建的 ATA
		solana.Meta(owner),                  // ATA 的所有者钱包
		solana.Meta(mint),                   // Token Mint
		solana.Meta(solana.SystemProgramID), // 创建账户需要 System Program
		solana.Meta(solana.TokenProgramID),  // 初始化 Token Account 需要 Token Program
	}

	// 3. 指令数据：0 = Create，1 = CreateIdempotent（账户已存在时不报错）
	// solana-go v1.12.0 只提供了 Create，这里直接用通用指令构造 Idempotent 版本
	ix := solana.NewInstruction(solana.SPLAssociatedTokenAccountProgramID, accounts, []byte{1})

	return ata, ix, nil
}
