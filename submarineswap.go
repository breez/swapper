package submarineswap

import (
	"crypto/sha256"
	"errors"
	"log"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
	"github.com/lightningnetwork/lnd/input"
)

const (
	defaultLockHeight      = 288
	redeemWitnessInputSize = 1 + 1 + 73 + 1 + 32 + 1 + 100
	refundWitnessInputSize = 1 + 1 + 73 + 1 + 0 + 1 + 100
)

var (
	submarineBucket      = []byte("submarineTransactions")
	wtxmgrNamespaceKey   = []byte("wtxmgr")
	waddrmgrNamespaceKey = []byte("waddrmgr")
)

func genSubmarineSwapScript(swapperPubKey, payerPubKey, hash []byte, lockHeight int64) ([]byte, error) {
	builder := txscript.NewScriptBuilder()

	builder.AddOp(txscript.OP_HASH160)
	builder.AddData(input.Ripemd160H(hash))
	builder.AddOp(txscript.OP_EQUAL) // Leaves 0P1 (true) on the stack if preimage matches
	builder.AddOp(txscript.OP_IF)
	builder.AddData(swapperPubKey) // Path taken if preimage matches
	builder.AddOp(txscript.OP_ELSE)
	builder.AddInt64(lockHeight)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)
	builder.AddData(payerPubKey) // Refund back to payer
	builder.AddOp(txscript.OP_ENDIF)
	builder.AddOp(txscript.OP_CHECKSIG)

	return builder.Script()
}

func NewSubmarineSwap(net *chaincfg.Params, pubKey, hash []byte) (address btcutil.Address, script, swapperPubKey []byte, lockHeight int64, err error) {

	err = pgConnect()
	if err != nil {
		log.Fatalf("pgConnect() error: %v", err)
	}

	if len(pubKey) != btcec.PubKeyBytesLenCompressed {
		err = errors.New("pubKey not valid")
		return
	}

	if len(hash) != 32 {
		err = errors.New("hash not valid")
		return
	}
	//Need to check that the hash doesn't already exists in our db
	_, _, _, _, errGet := getSwapperSubmarineData(hash)
	if errGet == nil {
		err = errors.New("Hash already exists")
		return
	}
	//Create swapperKey and swapperPubKey
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		return
	}
	swapperKey := key.Serialize()
	swapperPubKey = key.PubKey().SerializeCompressed()
	lockHeight = defaultLockHeight

	//Create the script
	script, err = genSubmarineSwapScript(swapperPubKey, pubKey, hash, defaultLockHeight)
	if err != nil {
		return
	}

	address, err = newAddressWitnessScriptHash(script, net)
	if err != nil {
		return
	}

	//Need to save the data into postgres
	err = saveSwapperSubmarineData(net.ScriptHashAddrID, hash, lockHeight, swapperKey, script)

	return
}
func newAddressWitnessScriptHash(script []byte, net *chaincfg.Params) (*btcutil.AddressWitnessScriptHash, error) {
	witnessProg := sha256.Sum256(script)
	return btcutil.NewAddressWitnessScriptHash(witnessProg[:], net)
}
