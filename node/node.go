package node

import (
	"context"
	"log"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/node/modules/dtypes"
	"github.com/ipfs/go-cid"
)

type NodeInterface interface {
	GetActor(id string) (*types.Actor, error)
	GetPending() ([]*types.SignedMessage, error)
	GetMessage(cidcc string) (*types.Message, error)
	StateSearchMsg(id string) (*api.MsgLookup, error)
	MsigGetPending(addr string) ([]*api.MsigTransaction, error)
	// SearchState(ctx context.Context, match Match, limit *int, offset *int, height int) ([]*SearchStateStruct, int, error)
	StateListMessages(ctx context.Context, addr string, lookback int) ([]*api.InvocResult, error)
	StateReplay(ctx context.Context, p1 types.TipSetKey, p2 cid.Cid) (*api.InvocResult, error)

	ChainHeadSub(ctx context.Context) (<-chan []*api.HeadChange, error)
	MpoolSub(ctx context.Context) (<-chan api.MpoolUpdate, error)
	Node() *Node
}

type Node struct {
	//api1 api.FullNodeStruct
	// closer jsonrpc.ClientCloser
	// api    v0api.FullNodeStruct
	// cache *ristretto.Cache
	// db     postgres.Database
	// ticker *time.Ticker
}

type SearchStateStruct struct {
	Tipset  *types.TipSet
	Message api.InvocResult
}

func (t *Node) Node() *Node {
	return t
}

func (t *Node) Connect(address1 string, token string) (dtypes.NetworkName, error) {
	lotus := GetLotusInstance(&LotusOptions{address: address1, token: token})

	name, _ := lotus.api.StateNetworkName(context.Background())
	log.Println("lotus network: ", name)
	if name == "mainnet" {
		address.CurrentNetwork = address.Mainnet
		log.Println("address network : mainnet")
	} else {
		address.CurrentNetwork = address.Testnet
		log.Println("address network : testnet")
	}
	return name, nil
}

func (t *Node) Close() {
	if lotus.closer != nil {
		lotus.closer()
	}
}

func (t *Node) ChainHead(ctx context.Context) (*types.TipSet, error) {
	tipset, err := lotus.api.ChainHead(ctx)
	return tipset, err
}

func (t *Node) ChainHeadSub(ctx context.Context) (<-chan []*api.HeadChange, error) {
	headchange, err := lotus.api.ChainNotify(ctx)
	return headchange, err
}

func (t *Node) MpoolSub(ctx context.Context) (<-chan api.MpoolUpdate, error) {
	mpool, err := lotus.api.MpoolSub(ctx)
	return mpool, err
}

func (t *Node) GetPending() ([]*types.SignedMessage, error) {

	tipset, err := lotus.api.ChainHead(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	status, err := lotus.api.MpoolPending(context.Background(), tipset.Key())
	if err != nil {
		log.Fatal(err)
	}

	return status, nil
}
