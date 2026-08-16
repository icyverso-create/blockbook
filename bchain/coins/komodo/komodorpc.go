package komodo

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/golang/glog"
	"github.com/juju/errors"
	"github.com/trezor/blockbook/bchain"
	"github.com/trezor/blockbook/bchain/coins/btc"
	"github.com/trezor/blockbook/common"
)

// KomodoRPC is an interface to JSON-RPC komodod service
type KomodoRPC struct {
	*btc.BitcoinRPC
}

// ResGetBlockChainInfo is a response to GetChainInfo request.
// komodod does not report size_on_disk, the remaining fields match bitcoind.
type ResGetBlockChainInfo struct {
	Error  *bchain.RPCError `json:"error"`
	Result struct {
		Chain         string            `json:"chain"`
		Blocks        int               `json:"blocks"`
		Headers       int               `json:"headers"`
		Bestblockhash string            `json:"bestblockhash"`
		Difficulty    common.JSONNumber `json:"difficulty"`
		Pruned        bool              `json:"pruned"`
		SizeOnDisk    int64             `json:"size_on_disk"`
		Consensus     struct {
			Chaintip  string `json:"chaintip"`
			Nextblock string `json:"nextblock"`
		} `json:"consensus"`
	} `json:"result"`
}

// NewKomodoRPC returns new KomodoRPC instance
func NewKomodoRPC(config json.RawMessage, pushHandler func(bchain.NotificationType)) (bchain.BlockChain, error) {
	b, err := btc.NewBitcoinRPC(config, pushHandler)
	if err != nil {
		return nil, err
	}
	k := &KomodoRPC{
		BitcoinRPC: b.(*btc.BitcoinRPC),
	}
	k.RPCMarshaler = JSONMarshalerV1Komodo{}
	// komodod has no estimatesmartfee ("Method not found"), and its estimatefee
	// never gathers enough data - it answers -1 for every target - so neither
	// estimator is advertised rather than leaking a negative fee to clients
	k.ChainConfig.SupportsEstimateSmartFee = false
	k.ChainConfig.SupportsEstimateFee = false
	return k, nil
}

// Initialize initializes KomodoRPC instance
func (k *KomodoRPC) Initialize() error {
	ci, err := k.GetChainInfo()
	if err != nil {
		return err
	}
	chainName := ci.Chain

	params := GetChainParams(chainName)

	k.Parser = NewKomodoParser(params, k.ChainConfig)

	// parameters for getInfo request
	if params.Net == MainnetMagic {
		k.Testnet = false
		k.Network = "livenet"
	} else {
		k.Testnet = true
		k.Network = "testnet"
	}

	glog.Info("rpc: block chain ", params.Name)

	return nil
}

// GetChainInfo returns info about the blockchain
func (k *KomodoRPC) GetChainInfo() (*bchain.ChainInfo, error) {
	chainInfo := ResGetBlockChainInfo{}
	err := k.Call(&btc.CmdGetBlockChainInfo{Method: "getblockchaininfo"}, &chainInfo)
	if err != nil {
		return nil, err
	}
	if chainInfo.Error != nil {
		return nil, chainInfo.Error
	}

	networkInfo := btc.ResGetNetworkInfo{}
	err = k.Call(&btc.CmdGetNetworkInfo{Method: "getnetworkinfo"}, &networkInfo)
	if err != nil {
		return nil, err
	}
	if networkInfo.Error != nil {
		return nil, networkInfo.Error
	}

	return &bchain.ChainInfo{
		Bestblockhash:   chainInfo.Result.Bestblockhash,
		Blocks:          chainInfo.Result.Blocks,
		Chain:           chainInfo.Result.Chain,
		Difficulty:      string(chainInfo.Result.Difficulty),
		Headers:         chainInfo.Result.Headers,
		SizeOnDisk:      chainInfo.Result.SizeOnDisk,
		Version:         string(networkInfo.Result.Version),
		Subversion:      string(networkInfo.Result.Subversion),
		ProtocolVersion: string(networkInfo.Result.ProtocolVersion),
		Timeoffset:      networkInfo.Result.Timeoffset,
		Consensus:       chainInfo.Result.Consensus,
		Warnings:        networkInfo.Result.Warnings,
	}, nil
}

// GetBlock returns block with given hash.
//
// komodod supports getblock verbosity 2 and — unlike zcashd — already includes
// the txid in every returned transaction, so no extra verbosity=1 round trip is
// needed to backfill them.
func (k *KomodoRPC) GetBlock(hash string, height uint32) (*bchain.Block, error) {
	type rpcBlock struct {
		bchain.BlockHeader
		Txs []bchain.Tx `json:"tx"`
	}
	type resGetBlockV2 struct {
		Error  *bchain.RPCError `json:"error"`
		Result rpcBlock         `json:"result"`
	}

	var err error
	if hash == "" && height > 0 {
		hash, err = k.GetBlockHash(height)
		if err != nil {
			return nil, err
		}
	}

	var rawResponse json.RawMessage
	req := btc.CmdGetBlock{Method: "getblock"}
	req.Params.BlockHash = hash
	req.Params.Verbosity = 2
	err = k.Call(&req, &rawResponse)
	if err != nil {
		return nil, errors.Annotatef(err, "hash %v", hash)
	}
	// getblock reports output amounts in "valueZat" while getrawtransaction uses
	// "valueSat" for the very same field, so normalize the block response
	rawResponse = bytes.ReplaceAll(rawResponse, []byte(`"valueZat"`), []byte(`"valueSat"`))

	res := resGetBlockV2{}
	if err = json.Unmarshal(rawResponse, &res); err != nil {
		return nil, errors.Annotatef(err, "hash %v", hash)
	}
	if res.Error != nil {
		return nil, errors.Annotatef(res.Error, "hash %v", hash)
	}

	return &bchain.Block{
		BlockHeader: res.Result.BlockHeader,
		Txs:         res.Result.Txs,
	}, nil
}

// GetTransaction returns a transaction by the transaction ID
func (k *KomodoRPC) GetTransaction(txid string) (*bchain.Tx, error) {
	r, err := k.getRawTransaction(txid)
	if err != nil {
		return nil, err
	}
	// getrawtransaction already returns "valueSat"; the replace keeps the two
	// code paths identical should a komodod build ever answer with "valueZat"
	r = bytes.ReplaceAll(r, []byte(`"valueZat"`), []byte(`"valueSat"`))
	tx, err := k.Parser.ParseTxFromJson(r)
	if err != nil {
		return nil, errors.Annotatef(err, "txid %v", txid)
	}
	tx.Blocktime = tx.Time
	tx.Txid = txid
	tx.CoinSpecificData = r
	return tx, nil
}

// getRawTransaction returns json as returned by backend, with all coin specific data
func (k *KomodoRPC) getRawTransaction(txid string) (json.RawMessage, error) {
	glog.V(1).Info("rpc: getrawtransaction ", txid)

	res := btc.ResGetRawTransaction{}
	req := btc.CmdGetRawTransaction{Method: "getrawtransaction"}
	req.Params.Txid = txid
	req.Params.Verbose = true
	err := k.Call(&req, &res)

	if err != nil {
		return nil, errors.Annotatef(err, "txid %v", txid)
	}
	if res.Error != nil {
		if btc.IsMissingTx(res.Error) {
			return nil, bchain.ErrTxNotFound
		}
		return nil, errors.Annotatef(res.Error, "txid %v", txid)
	}
	return res.Result, nil
}

// GetTransactionForMempool returns a transaction by the transaction ID.
// It could be optimized for mempool, i.e. without block time and confirmations
func (k *KomodoRPC) GetTransactionForMempool(txid string) (*bchain.Tx, error) {
	return k.GetTransaction(txid)
}

// GetMempoolEntry is not supported, komodod answers "Method not found"
func (k *KomodoRPC) GetMempoolEntry(txid string) (*bchain.MempoolEntry, error) {
	return nil, errors.New("GetMempoolEntry: not implemented")
}

// GetBlockRaw returns the block as a hex string. komodod implements getblock
// verbosity 0, so the raw block is available even though its transactions
// cannot be decoded from it by the btcd wire reader.
func (k *KomodoRPC) GetBlockRaw(hash string) (string, error) {
	glog.V(1).Info("rpc: getblock (verbosity=0) ", hash)

	res := btc.ResGetBlockRaw{}
	req := btc.CmdGetBlock{Method: "getblock"}
	req.Params.BlockHash = hash
	req.Params.Verbosity = 0
	err := k.Call(&req, &res)

	if err != nil {
		return "", errors.Annotatef(err, "hash %v", hash)
	}
	if res.Error != nil {
		if btc.IsErrBlockNotFound(res.Error) {
			return "", bchain.ErrBlockNotFound
		}
		return "", errors.Annotatef(res.Error, "hash %v", hash)
	}
	return res.Result, nil
}

// JSONMarshalerV1Komodo marshals requests for komodod, which accepts only
// positional parameters and needs the getblock verbosity as a number - the
// generic V1 marshaler would send it as a bool, downgrading verbosity 2 to 1
// and returning transactions as bare txid strings.
type JSONMarshalerV1Komodo struct{}

// Marshal converts struct passed by parameter to JSON
func (JSONMarshalerV1Komodo) Marshal(v interface{}) ([]byte, error) {
	u := cmdUntypedParams{}

	switch v := v.(type) {
	case *btc.CmdGetBlock:
		u.Method = v.Method
		u.Params = append(u.Params, v.Params.BlockHash)
		u.Params = append(u.Params, v.Params.Verbosity)
	case *btc.CmdGetRawTransaction:
		var n int
		if v.Params.Verbose {
			n = 1
		}
		u.Method = v.Method
		u.Params = append(u.Params, v.Params.Txid)
		u.Params = append(u.Params, n)
	default:
		{
			v := reflect.ValueOf(v).Elem()

			f := v.FieldByName("Method")
			if !f.IsValid() || f.Kind() != reflect.String {
				return nil, btc.ErrInvalidValue
			}
			u.Method = f.String()

			f = v.FieldByName("Params")
			if f.IsValid() {
				var arr []interface{}
				switch f.Kind() {
				case reflect.Slice:
					arr = make([]interface{}, f.Len())
					for i := 0; i < f.Len(); i++ {
						arr[i] = f.Index(i).Interface()
					}
				case reflect.Struct:
					arr = make([]interface{}, f.NumField())
					for i := 0; i < f.NumField(); i++ {
						arr[i] = f.Field(i).Interface()
					}
				default:
					return nil, btc.ErrInvalidValue
				}
				u.Params = arr
			}
		}
	}
	u.Id = "-"
	if u.Params == nil {
		u.Params = make([]interface{}, 0)
	}
	return json.Marshal(u)
}

type cmdUntypedParams struct {
	Method string        `json:"method"`
	Id     string        `json:"id"`
	Params []interface{} `json:"params"`
}
