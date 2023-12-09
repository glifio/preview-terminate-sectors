package node

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/filecoin-project/go-jsonrpc"
	"github.com/filecoin-project/lotus/api"
)

var once sync.Once

type LotusNode struct {
	api    api.FullNodeStruct
	closer jsonrpc.ClientCloser
}

type LotusOptions struct {
	address string
	token   string
}

// variabel Global
var lotus *LotusNode

func GetLotusInstance(opts *LotusOptions) *LotusNode {

	once.Do(func() {
		lotus = &LotusNode{}

		head := http.Header{}

		if opts.token != "" {
			head.Set("Authorization", "Bearer "+opts.token)
		}

		var err error
		closer, err := jsonrpc.NewMergeClient(context.Background(),
			opts.address,
			"Filecoin",
			api.GetInternalStructs(&lotus.api),
			head)
		if err != nil {
			log.Fatalf("connecting with lotus failed: %s", err)
		}

		// Print client and server version numbers.
		ts, err := lotus.api.ChainHead(context.Background())
		if err != nil {
			log.Fatalf("getting chainhead from lotus failed: %s", err)
		}
		log.Printf("lotus client: %s", opts.address)
		log.Printf("lotus chainhead: %d", ts.Height())
		log.Println(strings.Repeat("~", 37))

		lotus.closer = closer
	})

	return lotus
}

func (node *LotusNode) Close() {
	if node.closer != nil {
		node.closer()
	}
}

func Lotus() api.FullNodeStruct {
	return lotus.api
}
