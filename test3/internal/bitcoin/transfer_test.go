package bitcoin

import (
	"context"
	"strings"
	"testing"

	"btctransfer/internal/mpc"
)

const testnetAddr = "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx"

// wrongKeySigner 返回的公钥与签名私钥不匹配，模拟签名机异常。
type wrongKeySigner struct{ *mpc.MockMPCSigner }

func (s wrongKeySigner) Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error) {
	return mpc.NewMockMPCSigner("other").Sign(ctx, "other", digest)
}

func TestBitcoinTransfer(t *testing.T) {
	ctx := context.Background()
	signer := mpc.NewMockMPCSigner(KeyID)

	// 正常转账：签名通过验签和脚本引擎校验，返回 txid
	txid, err := BitcoinTransfer(ctx, testnetAddr, 50_000, signer)
	if err != nil {
		t.Fatal(err)
	}
	if len(txid) != 64 {
		t.Fatalf("txid = %q, want 64 hex chars", txid)
	}

	cases := []struct {
		name    string
		addr    string
		amount  int64
		signer  mpc.Signer
		wantErr string
	}{
		{"主网地址", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", 50_000, signer, "not for"},
		{"非法地址", "not-an-address", 50_000, signer, "invalid btc address"},
		{"低于 dust", testnetAddr, 100, signer, "dust"},
		{"余额不足", testnetAddr, 200_000, signer, "insufficient funds"},
		{"未知 keyID", testnetAddr, 50_000, mpc.NewMockMPCSigner("other"), "unknown keyID"},
		{"签名与公钥不匹配", testnetAddr, 50_000, wrongKeySigner{signer}, "verify failed"},
	}
	for _, c := range cases {
		_, err := BitcoinTransfer(ctx, c.addr, c.amount, c.signer)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want contains %q", c.name, err, c.wantErr)
		}
	}
}
