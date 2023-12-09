package node

import (
	"context"
	"log"
	"math/big"
	"sync"

	"github.com/glifio/go-pools/sdk"
	"github.com/glifio/go-pools/types"
)

var PoolsSDK types.PoolsSDK
var PoolsArchiveSDK types.PoolsSDK

var initSDKOnce sync.Once
var initArchiveSDKOnce sync.Once

func InitPoolsSDK(
	ctx context.Context,
	chainID int64,
	dialAddr string,
	token string,
) {
	initSDKOnce.Do(func() {
		sdk, err := sdk.New(ctx, big.NewInt(chainID), types.Extern{
			AdoAddr:       "https://ado.glif.link/rpc/v0",
			LotusDialAddr: dialAddr,
			LotusToken:    token,
		})
		if err != nil {
			log.Fatal("node connection error: ", err)
		}
		PoolsSDK = sdk
	})
}

func InitPoolsArchiveSDK(
	ctx context.Context,
	chainID int64,
	dialAddr string,
	token string,
) {
	initArchiveSDKOnce.Do(func() {
		sdk, err := sdk.New(ctx, big.NewInt(chainID), types.Extern{
			AdoAddr:       "",
			LotusDialAddr: dialAddr,
			LotusToken:    token,
		})
		if err != nil {
			log.Fatal("node connection error: ", err)
		}
		PoolsArchiveSDK = sdk
	})
}
