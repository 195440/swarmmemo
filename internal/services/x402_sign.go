package services

// EIP-712 typed-data signing of EIP-3009 transferWithAuthorization, the
// payment the x402 "exact" scheme uses on EVM chains. Keccak-256 is
// implemented here (the standard library has only FIPS SHA-3, which pads
// differently); secp256k1 signing is btcec's, the module the Nostr bridge
// already uses. No new dependency.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"swarmmemo/internal/pay"
)

// keccak256, keccakSponge, EVMAddress and ParseEVMAddress live in
// internal/pay (C202a), which both payment wires share; these names keep
// every caller and test here unchanged.

// keccak256 is the legacy Keccak-256 Ethereum uses (domain byte 0x01).
func keccak256(parts ...[]byte) [32]byte { return pay.Keccak256(parts...) }

// keccakSponge is pay.KeccakSponge: domain byte 0x01 is Keccak-256, 0x06
// FIPS SHA3-256.
func keccakSponge(ds byte, parts ...[]byte) [32]byte { return pay.KeccakSponge(ds, parts...) }

// EVMAddress is a 20-byte account or contract address (pay.EVMAddress).
type EVMAddress = pay.EVMAddress

// ParseEVMAddress reads "0x" followed by 40 hex digits. Mixed case must be a
// valid EIP-55 checksum; all-lower or all-upper is accepted as is.
func ParseEVMAddress(s string) (EVMAddress, bool) { return pay.ParseEVMAddress(s) }

// EIP712Domain is the domain of an EIP-3009 token: its EIP-712 name and
// version (x402 carries them in the requirement's "extra"), chain and
// contract.
type EIP712Domain struct {
	Name, Version     string
	ChainID           int64
	VerifyingContract EVMAddress
}

var (
	eip712DomainType = keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	// TransferWithAuthorizationTypeHash is EIP-3009's
	// TRANSFER_WITH_AUTHORIZATION_TYPEHASH.
	TransferWithAuthorizationTypeHash = keccak256([]byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"))
)

func word(n *big.Int) []byte {
	var w [32]byte
	n.FillBytes(w[:])
	return w[:]
}

func wordInt(n int64) []byte { return word(big.NewInt(n)) }

func wordAddress(a EVMAddress) []byte {
	var w [32]byte
	copy(w[12:], a[:])
	return w[:]
}

// Separator is the domain's hashStruct.
func (d EIP712Domain) Separator() [32]byte {
	name := keccak256([]byte(d.Name))
	version := keccak256([]byte(d.Version))
	return keccak256(eip712DomainType[:], name[:], version[:], wordInt(d.ChainID), wordAddress(d.VerifyingContract))
}

// TransferAuthorization is the EIP-3009 message. Value is in the token's
// atomic units; ValidAfter and ValidBefore are Unix seconds.
type TransferAuthorization struct {
	From, To                EVMAddress
	Value                   int64
	ValidAfter, ValidBefore int64
	Nonce                   [32]byte
}

// Digest is keccak256(0x1901 ‖ domain separator ‖ hashStruct(message)).
func (a TransferAuthorization) Digest(d EIP712Domain) [32]byte {
	sep := d.Separator()
	msg := keccak256(TransferWithAuthorizationTypeHash[:], wordAddress(a.From), wordAddress(a.To), wordInt(a.Value), wordInt(a.ValidAfter), wordInt(a.ValidBefore), a.Nonce[:])
	return keccak256([]byte{0x19, 0x01}, sep[:], msg[:])
}

// X402Signer signs EIP-3009 transfer authorizations for the x402 wallet. It
// signs nothing else: no raw digests, no transactions. A signer never
// returns, logs or formats its key.
type X402Signer interface {
	Address() EVMAddress
	SignAuthorization(d EIP712Domain, a TransferAuthorization) ([65]byte, error)
}

// keySigner holds a secp256k1 key in memory.
type keySigner struct {
	key  *btcec.PrivateKey
	addr EVMAddress
}

func (s *keySigner) Address() EVMAddress { return s.addr }

// String and GoString keep the key out of any %v, %+v or %#v.
func (s *keySigner) String() string   { return "x402 signer " + s.addr.String() }
func (s *keySigner) GoString() string { return s.String() }

var errSign = errors.New("x402: signing failed")

// SignAuthorization returns r ‖ s ‖ v with v 27 or 28, as EIP-3009 and the
// x402 "exact" payload expect. The authorization must be from this wallet.
func (s *keySigner) SignAuthorization(d EIP712Domain, a TransferAuthorization) ([65]byte, error) {
	var out [65]byte
	if a.From != s.addr || a.Value <= 0 || a.ValidBefore <= a.ValidAfter {
		return out, errSign
	}
	digest := a.Digest(d)
	return signDigest(s.key, digest)
}

func signDigest(key *btcec.PrivateKey, digest [32]byte) ([65]byte, error) {
	var out [65]byte
	compact := btcecdsa.SignCompact(key, digest[:], false) // [27+recid] ‖ r ‖ s, RFC 6979, low s
	if len(compact) != 65 || compact[0] < 27 || compact[0] > 28 {
		return out, errSign
	}
	copy(out[:64], compact[1:])
	out[64] = compact[0]
	return out, nil
}

// recoverAddress returns the address that signed digest (tests and the
// self-check after signing use it).
func recoverAddress(digest [32]byte, sig [65]byte) (EVMAddress, bool) {
	var compact [65]byte
	compact[0] = sig[64]
	copy(compact[1:], sig[:64])
	pub, _, err := btcecdsa.RecoverCompact(compact[:], digest[:])
	if err != nil {
		return EVMAddress{}, false
	}
	return pubAddress(pub), true
}

func pubAddress(pub *btcec.PublicKey) EVMAddress {
	h := keccak256(pub.SerializeUncompressed()[1:])
	var a EVMAddress
	copy(a[:], h[12:])
	return a
}

func signerFromKeyBytes(b []byte) (*keySigner, error) {
	if len(b) != 32 {
		return nil, errKeyFile
	}
	var n big.Int
	n.SetBytes(b)
	if n.Sign() == 0 || n.Cmp(btcec.S256().Params().N) >= 0 {
		return nil, errKeyFile
	}
	key, _ := btcec.PrivKeyFromBytes(b)
	return &keySigner{key: key, addr: pubAddress(key.PubKey())}, nil
}

// errKeyFile is the one error a key file produces: it never quotes the
// file's content or the reason a byte was refused.
var errKeyFile = errors.New("x402: wallet key file must be 64 hex digits (optionally 0x-prefixed), a valid secp256k1 key, a regular file readable by its owner only")

// LoadX402Signer reads the wallet key from path: 64 hex digits, optionally
// 0x-prefixed, surrounding whitespace ignored. The file must be a regular
// file that neither group nor others can read.
func LoadX402Signer(path string) (X402Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("x402: wallet key file does not exist")
		}
		return nil, errKeyFile
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 256 {
		return nil, errKeyFile
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errKeyFile
	}
	defer clear(raw)
	text := strings.TrimSpace(string(raw))
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	if len(text) != 64 {
		return nil, errKeyFile
	}
	var key [32]byte
	defer clear(key[:])
	if _, err = hex.Decode(key[:], []byte(text)); err != nil {
		return nil, errKeyFile
	}
	return signerFromKeyBytes(key[:])
}

// WriteX402Key writes a fresh random wallet key to path (mode 0600, never
// overwriting) and returns its address. It is the operator's keygen: the
// wallet is a hot wallet that holds only the day's float.
func WriteX402Key(path string) (EVMAddress, error) {
	var key [32]byte
	defer clear(key[:])
	var s *keySigner
	for s == nil {
		if _, err := rand.Read(key[:]); err != nil {
			return EVMAddress{}, err
		}
		s, _ = signerFromKeyBytes(key[:])
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return EVMAddress{}, err
	}
	text := []byte(hex.EncodeToString(key[:]) + "\n")
	defer clear(text)
	_, err = f.Write(text)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return EVMAddress{}, err
	}
	return s.addr, nil
}
