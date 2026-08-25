package bsv

import (
	"bytes"
	"encoding/hex"
	"math/big"

	"github.com/martinboehm/btcd/wire"
	"github.com/martinboehm/btcutil/txscript"
	"github.com/trezor/blockbook/bchain"
)

// ParseBlock parses a raw block, keeping output scripts as bytes instead of
// re-encoding them to hex.
//
// The stock BitcoinLikeParser.ParseBlock hex-encodes every scriptSig and every
// scriptPubKey (bitcoinlikeparser.go TxFromMsgTx), and the indexer then decodes
// the scriptPubKey straight back to bytes in GetAddrDescFromVout. That round
// trip costs two extra bytes of string per byte of script and is invisible on
// chains with small blocks. On Bitcoin SV, where the large blocks are large
// precisely because they carry data inside scripts, it is the dominant memory
// cost: on a measured 3814 MiB block the scripts alone came to several GiB of
// strings that nothing ever reads as strings.
//
// So here scriptPubKey goes into Vout.ScriptPubKey.Script as a plain slice
// aliasing the already-parsed block (no copy at all), and scriptSig is dropped
// entirely - the bitcoin-type sync path reads only the outpoint from an input,
// never its script, and the public API serves transactions from the database
// and from getrawtransaction, never from a parsed block.
func (p *BSVParser) ParseBlock(b []byte) (*bchain.Block, error) {
	w := wire.MsgBlock{}
	if err := w.Deserialize(bytes.NewReader(b)); err != nil {
		return nil, err
	}

	txs := make([]bchain.Tx, len(w.Transactions))
	for ti, t := range w.Transactions {
		txs[ti] = p.txFromMsgTx(t)
	}

	return &bchain.Block{
		BlockHeader: bchain.BlockHeader{
			Prev: w.Header.PrevBlock.String(), // needed for fork detection when parsing raw blocks
			Size: len(b),
			Time: w.Header.Timestamp.Unix(),
		},
		Txs: txs,
	}, nil
}

// txFromMsgTx is the sync-path counterpart of BitcoinLikeParser.TxFromMsgTx.
// It fills exactly the fields db.processAddressesBitcoinType consumes.
func (p *BSVParser) txFromMsgTx(t *wire.MsgTx) bchain.Tx {
	vin := make([]bchain.Vin, len(t.TxIn))
	for i, in := range t.TxIn {
		if blockchainIsCoinBaseTx(t) {
			// the coinbase script is bounded to 100 bytes by consensus, so
			// keeping it as hex costs nothing and preserves the stock shape
			vin[i] = bchain.Vin{
				Coinbase: hex.EncodeToString(in.SignatureScript),
				Sequence: in.Sequence,
			}
			break
		}
		vin[i] = bchain.Vin{
			Txid:     in.PreviousOutPoint.Hash.String(),
			Vout:     in.PreviousOutPoint.Index,
			Sequence: in.Sequence,
			// ScriptSig deliberately left empty, see ParseBlock
		}
	}

	vout := make([]bchain.Vout, len(t.TxOut))
	for i, out := range t.TxOut {
		var vs big.Int
		vs.SetInt64(out.Value)
		vout[i] = bchain.Vout{
			ValueSat: vs,
			N:        uint32(i),
			ScriptPubKey: bchain.ScriptPubKey{
				Script:    out.PkScript,
				Addresses: []string{},
			},
		}
	}

	return bchain.Tx{
		Txid:     t.TxHash().String(),
		Version:  t.Version,
		LockTime: t.LockTime,
		Vin:      vin,
		Vout:     vout,
	}
}

// GetAddrDescFromVout returns the address descriptor of an output, preferring
// the raw script left by ParseBlock and falling back to the hex field for
// outputs that came from elsewhere (getrawtransaction, mempool).
func (p *BSVParser) GetAddrDescFromVout(output *bchain.Vout) (bchain.AddressDescriptor, error) {
	script := output.ScriptPubKey.Script
	if script == nil {
		var err error
		script, err = hex.DecodeString(output.ScriptPubKey.Hex)
		if err != nil {
			return nil, err
		}
	}
	// convert possible P2PK script to P2PKH so that all transactions by a given
	// public key are indexed together, exactly as the stock parser does
	return txscript.ConvertP2PKtoP2PKH(p.Params.Base58CksumHasher, script)
}

// blockchainIsCoinBaseTx mirrors blockchain.IsCoinBaseTx without pulling the
// whole package in: a coinbase has exactly one input whose outpoint is null.
func blockchainIsCoinBaseTx(msgTx *wire.MsgTx) bool {
	if len(msgTx.TxIn) != 1 {
		return false
	}
	prevOut := &msgTx.TxIn[0].PreviousOutPoint
	return prevOut.Index == ^uint32(0) && prevOut.Hash == zeroHash
}

var zeroHash [32]byte
