package submarineswap

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
	"github.com/btcsuite/btcwallet/chain"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/btcsuite/btcwallet/walletdb"
	"github.com/lightningnetwork/lnd/channeldb"
	"github.com/lightningnetwork/lnd/input"
	"github.com/lightningnetwork/lnd/kvdb"
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

func saveSwapperSubmarineData(c *channeldb.ChannelStateDB, netID byte, hash []byte, creationHeight, lockHeight int64, swapperKey []byte, script []byte) error {

	/**
	key: swapper:<hash>
	value:
		[0]: netID
		[1:9]: creationHeight
		[9:17]: lockHeight
		[17:17+btcec.PrivKeyBytesLen]: swapperKey
		[17+btcec.PrivKeyBytesLen:]: script
	*/

	if len(swapperKey) != btcec.PrivKeyBytesLen {
		return errors.New("swapperKey not valid")
	}

	return kvdb.Update(c.GetParentDB(), func(tx walletdb.ReadWriteTx) error {
		bucket, err := tx.CreateTopLevelBucket(submarineBucket)
		if err != nil {
			return err
		}

		var key bytes.Buffer
		_, err = key.WriteString("swapper:")
		if err != nil {
			return err
		}
		_, err = key.Write(hash)
		if err != nil {
			return err
		}

		var submarineData bytes.Buffer
		err = submarineData.WriteByte(netID)
		if err != nil {
			return err
		}
		b := make([]byte, 16)
		binary.BigEndian.PutUint64(b[0:], uint64(creationHeight))
		binary.BigEndian.PutUint64(b[8:], uint64(lockHeight))
		_, err = submarineData.Write(b)
		if err != nil {
			return err
		}
		_, err = submarineData.Write(swapperKey)
		if err != nil {
			return err
		}
		_, err = submarineData.Write(script)
		if err != nil {
			return err
		}

		return bucket.Put(key.Bytes(), submarineData.Bytes())
	}, func() {})
}

func getSwapperSubmarineData(c *channeldb.ChannelStateDB, netID byte, hash []byte) (creationHeight, lockHeight int64, swapperKey, script []byte, err error) {

	err = kvdb.View(c.GetParentDB(), func(tx walletdb.ReadTx) error {

		bucket := tx.ReadBucket(submarineBucket)
		if bucket == nil {
			return errors.New("Not found")
		}

		var key bytes.Buffer
		_, err = key.WriteString("swapper:")
		if err != nil {
			return err
		}
		_, err = key.Write(hash)
		if err != nil {
			return err
		}

		value := bucket.Get(key.Bytes())
		if value == nil {
			return errors.New("Not found")
		}

		submarineData := bytes.NewBuffer(value)
		savedNetID, err := submarineData.ReadByte()
		if err != nil {
			return err
		}
		if savedNetID != netID {
			return errors.New("Not the same network")
		}
		b := make([]byte, 16)
		_, err = submarineData.Read(b)
		if err != nil {
			return err
		}
		creationHeight = int64(binary.BigEndian.Uint64(b[0:]))
		lockHeight = int64(binary.BigEndian.Uint64(b[8:]))

		swapperKey = make([]byte, btcec.PrivKeyBytesLen)
		_, err = submarineData.Read(swapperKey)
		if err != nil {
			return err
		}

		script = make([]byte, submarineData.Len())
		_, err = submarineData.Read(script)
		if err != nil {
			return err
		}

		return nil
	}, func() {})

	return
}

func NewSubmarineSwap(wdb walletdb.DB, manager *waddrmgr.Manager, net *chaincfg.Params,
	chainClient chain.Interface, c *channeldb.ChannelStateDB, pubKey, hash []byte) (address btcutil.Address, script, swapperPubKey []byte, lockHeight int64, err error) {

	if len(pubKey) != btcec.PubKeyBytesLenCompressed {
		err = errors.New("pubKey not valid")
		return
	}

	if len(hash) != 32 {
		err = errors.New("hash not valid")
		return
	}

	//Need to check that the hash doesn't already exists in our db
	_, _, _, _, errGet := getSwapperSubmarineData(c, net.ScriptHashAddrID, hash)
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

	currentHash, currentHeight, err := chainClient.GetBestBlock()
	if err != nil {
		return
	}

	address, err = importScript(
		wdb,
		manager,
		net,
		currentHeight,
		*currentHash,
		script,
	)
	if err != nil {
		return
	}
	//Watch the new address
	err = chainClient.NotifyReceived([]btcutil.Address{address})
	if err != nil {
		return
	}
	//Need to save the data keyed by hash
	err = saveSwapperSubmarineData(c, net.ScriptHashAddrID, hash, int64(currentHeight), lockHeight, swapperKey, script)

	return
}
func newAddressWitnessScriptHash(script []byte, net *chaincfg.Params) (*btcutil.AddressWitnessScriptHash, error) {
	witnessProg := sha256.Sum256(script)
	return btcutil.NewAddressWitnessScriptHash(witnessProg[:], net)
}
func importScript(db walletdb.DB, manager *waddrmgr.Manager, net *chaincfg.Params, startHeight int32, startHash chainhash.Hash, script []byte) (btcutil.Address, error) {
	var p2wshAddr *btcutil.AddressWitnessScriptHash
	err := walletdb.Update(db, func(tx walletdb.ReadWriteTx) error {
		addrmgrNs := tx.ReadWriteBucket(waddrmgrNamespaceKey)

		bs := &waddrmgr.BlockStamp{
			Hash:   startHash,
			Height: startHeight,
		}

		// As this is a regular P2SH script, we'll import this into the
		// BIP0044 scope.
		bip44Mgr, err := manager.FetchScopedKeyManager(
			waddrmgr.KeyScopeBIP0084,
		)
		if err != nil {
			return err
		}

		addrInfo, err := bip44Mgr.ImportWitnessScript(addrmgrNs, script, bs, 0, false)
		if err != nil {
			if waddrmgr.IsError(err, waddrmgr.ErrDuplicateAddress) {
				p2wshAddr, _ = newAddressWitnessScriptHash(script, net)
				return nil
			}
			return err
		}

		p2wshAddr = addrInfo.Address().(*btcutil.AddressWitnessScriptHash)
		return nil
	})
	return p2wshAddr, err
}
