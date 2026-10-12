package mpc

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// Signer 是业务服务访问签名机的唯一入口：只提交 keyID + 32 字节摘要，拿回签名，
// 业务服务永远接触不到私钥或私钥分片。
type Signer interface {
	// Sign 返回 secp256k1 ECDSA 签名，DER 编码（low-S，不含 SIGHASH 字节）。
	Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error)
	// PublicKey 返回 33 字节压缩公钥。
	PublicKey(ctx context.Context, keyID string) ([]byte, error)
}

// MockMPCSigner 仅用于本地演示。
//
// !!! Mock 不等于真实 MPC !!!
// 这里为了让流程可以被密码学验证，进程内持有一把由固定种子派生的“测试私钥”做普通单方 ECDSA 签名，
// 签名是真实有效的 secp256k1 签名，但密钥安全模型与 MPC 完全不同。
//
// 真实 MPC（如 GG18/GG20/CGGMP）：
//   - 多个参与方各自持有私钥份额，完整私钥从未在任何地方生成或恢复；
//   - 各方运行分布式签名协议，最终组合出标准 ECDSA 签名；
//   - 签名机有独立的授权策略（白名单地址、限额、审批），并会自行解析/复核交易内容，
//     而不是盲签业务方给的摘要。
type MockMPCSigner struct {
	keys map[string]*btcec.PrivateKey
}

// NewMockMPCSigner 为每个 keyID 用固定测试种子派生一把私钥。不要用于真实资金。
func NewMockMPCSigner(keyIDs ...string) *MockMPCSigner {
	s := &MockMPCSigner{keys: map[string]*btcec.PrivateKey{}}
	for _, id := range keyIDs {
		seed := sha256.Sum256([]byte("mock-mpc-test-only:" + id))
		priv, _ := btcec.PrivKeyFromBytes(seed[:])
		s.keys[id] = priv
	}
	return s
}

func (s *MockMPCSigner) Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error) {
	priv, ok := s.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("mpc: unknown keyID %q", keyID)
	}
	if len(digest) != 32 {
		return nil, fmt.Errorf("mpc: digest must be 32 bytes, got %d", len(digest))
	}
	// 真实 MPC：此处向各参与方发起签名会话，并由策略引擎审批。
	return ecdsa.Sign(priv, digest).Serialize(), nil // RFC6979 + low-S + DER
}

func (s *MockMPCSigner) PublicKey(ctx context.Context, keyID string) ([]byte, error) {
	priv, ok := s.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("mpc: unknown keyID %q", keyID)
	}
	return priv.PubKey().SerializeCompressed(), nil
}
