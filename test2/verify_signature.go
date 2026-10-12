package main

import (
	"crypto/ed25519"

	"github.com/gagliardetto/solana-go"
)

// VerifySignature 用 Ed25519 对 Message 签名并验签，同时演示篡改 Message 后验签失败。
// 返回：签名、原始消息验签结果、篡改消息验签结果。
func VerifySignature(message []byte, privateKey solana.PrivateKey) (solana.Signature, bool, bool) {
	// 1. 用私钥对 Message 字节签名（Solana 私钥 64 字节 = seed + 公钥，与 Go 标准库格式一致）
	priv := ed25519.PrivateKey(privateKey)
	sig := ed25519.Sign(priv, message)

	// 2. 从私钥推导公钥
	pub := priv.Public().(ed25519.PublicKey)

	// 3. 用公钥验证原始 Message
	valid := ed25519.Verify(pub, message, sig)

	// 4. 复制并修改 Message 的一个字节，再验证 —— 应当失败
	tampered := append([]byte(nil), message...)
	tampered[len(tampered)-1] ^= 0xFF
	tamperedValid := ed25519.Verify(pub, tampered, sig)

	return solana.SignatureFromBytes(sig), valid, tamperedValid
}
