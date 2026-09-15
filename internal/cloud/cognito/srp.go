package cognito

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

// nHex is the 3072-bit safe prime Cognito uses for SRP-6a (RFC 5054 group).
const nHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
	"29024E088A67CC74020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245" +
	"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3D" +
	"C2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F" +
	"83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
	"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9" +
	"DE2BCBF6955817183995497CEA956AE515D2261898FA0510" +
	"15728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64" +
	"ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
	"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6B" +
	"F12FFA06D98A0864D87602733EC86A64521F2B18177B200CB" +
	"BE117577A615D6C770988C0BAD946E208E24FA074E5AB3143" +
	"DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

const derivedKeyInfo = "Caldera Derived Key"

var (
	srpN = mustHex(nHex)
	srpG = big.NewInt(2)
	srpK = hashHex(padHex(srpN) + padHex(srpG))
)

// srpSession holds the client half of one SRP-6a exchange.
type srpSession struct {
	poolName string
	a        *big.Int
	bigA     *big.Int
}

func newSRPSession(poolName string, random io.Reader) (*srpSession, error) {
	buf := make([]byte, 128)
	for {
		if _, err := io.ReadFull(random, buf); err != nil {
			return nil, fmt.Errorf("generating SRP secret: %w", err)
		}
		a := new(big.Int).SetBytes(buf)
		bigA := new(big.Int).Exp(srpG, a, srpN)
		if bigA.Sign() != 0 {
			return &srpSession{poolName: poolName, a: a, bigA: bigA}, nil
		}
	}
}

// PublicHex is SRP_A as Cognito expects it.
func (s *srpSession) PublicHex() string {
	return s.bigA.Text(16)
}

// Signature computes PASSWORD_CLAIM_SIGNATURE for a PASSWORD_VERIFIER challenge.
func (s *srpSession) Signature(userID, password, srpBHex, saltHex, secretBlock, timestamp string) (string, error) {
	bigB, ok := new(big.Int).SetString(srpBHex, 16)
	if !ok || new(big.Int).Mod(bigB, srpN).Sign() == 0 {
		return "", errors.New("invalid SRP_B from server")
	}

	u := hashHex(padHex(s.bigA) + padHex(bigB))
	if u.Sign() == 0 {
		return "", errors.New("SRP safety check failed: u is zero")
	}

	identity := sha256.Sum256([]byte(s.poolName + userID + ":" + password))
	x := hashHex(padHexString(saltHex) + hex.EncodeToString(identity[:]))

	gx := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(bigB, new(big.Int).Mul(srpK, gx))
	base.Mod(base, srpN)
	exp := new(big.Int).Add(s.a, new(big.Int).Mul(u, x))
	secret := new(big.Int).Exp(base, exp, srpN)

	key := deriveKey(secret, u)

	block, err := base64.StdEncoding.DecodeString(secretBlock)
	if err != nil {
		return "", fmt.Errorf("decoding SECRET_BLOCK: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(s.poolName))
	mac.Write([]byte(userID))
	mac.Write(block)
	mac.Write([]byte(timestamp))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// deriveKey is the single-block HKDF Cognito specifies, truncated to 16 bytes.
func deriveKey(secret, u *big.Int) []byte {
	prk := hmac.New(sha256.New, mustDecodeHex(padHex(u)))
	prk.Write(mustDecodeHex(padHex(secret)))

	mac := hmac.New(sha256.New, prk.Sum(nil))
	mac.Write([]byte(derivedKeyInfo + "\x01"))
	return mac.Sum(nil)[:16]
}

func hashHex(hexStr string) *big.Int {
	sum := sha256.Sum256(mustDecodeHex(hexStr))
	return new(big.Int).SetBytes(sum[:])
}

func padHex(n *big.Int) string {
	return padHexString(n.Text(16))
}

// padHexString makes hex an even length and prefixes 00 when the high bit is set,
// so the bytes read as a positive two's-complement integer.
func padHexString(h string) string {
	h = strings.TrimPrefix(h, "-")
	if len(h)%2 == 1 {
		h = "0" + h
	}
	if strings.ContainsRune("89abcdefABCDEF", rune(h[0])) {
		h = "00" + h
	}
	return h
}

func mustHex(h string) *big.Int {
	n, ok := new(big.Int).SetString(h, 16)
	if !ok {
		panic("cognito: invalid hex constant")
	}
	return n
}

func mustDecodeHex(h string) []byte {
	b, err := hex.DecodeString(h)
	if err != nil {
		panic("cognito: padHex produced invalid hex: " + err.Error())
	}
	return b
}
