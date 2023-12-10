package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"

	"github.com/filecoin-project/go-address"
	filtypes "github.com/filecoin-project/lotus/chain/types"
	"github.com/glifio/go-pools/types"
	"github.com/glifio/go-pools/util"
	"github.com/jimpick/preview-terminate-sectors/node"
	"github.com/rs/cors"
	"github.com/spf13/viper"
)

var height uint64 = 3461984

type JSONResult struct {
	Epoch             uint64
	Miner             address.Address
	SectorsTerminated uint64
	SectorsCount      uint64
	Balance           *big.Int
	TotalBurn         *big.Int
	LiquidationValue  *big.Int
	RecoveryRatio     float64
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	query := node.PoolsArchiveSDK.Query()

	minerID := "f01931245"

	var batchSize uint64 = 40
	var gasLimit uint64 = 270000000000
	var maxPartitions uint64 = 21

	sampleSectors := true
	optimize := true
	offchain := true

	tipset := fmt.Sprintf("@%d", height)

	/*
		fmt.Printf("got / request\n")
		for i := 0; i < 100; i++ {
			str := fmt.Sprintf("{\"seq\": %d}\n", i)
			io.WriteString(w, str)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(1 * time.Second)
		}
	*/
	minerAddr, err := address.NewFromString(minerID)
	if err != nil {
		// FIXME: 404
		log.Print(err)
		return
	}

	fmt.Println("---")

	errorCh := make(chan error)
	progressCh := make(chan *types.PreviewTerminateSectorsProgress)
	resultCh := make(chan *types.PreviewTerminateSectorsReturn)

	go query.PreviewTerminateSectors(context.Background(), minerAddr,
		tipset, height, batchSize, gasLimit, sampleSectors, optimize, offchain,
		maxPartitions, errorCh, progressCh, resultCh)

	epoch := height
	height = height - 12*60*2

	var actor *filtypes.ActorV5
	var totalBurn *big.Int
	var sectorsTerminated uint64
	var sectorsCount uint64
	var sectorsInSkippedPartitions uint64
	var partitionsCount uint64
	var sampledPartitionsCount uint64

loop:
	for {
		select {
		case result := <-resultCh:
			actor = result.Actor
			totalBurn = result.TotalBurn
			sectorsTerminated = result.SectorsTerminated
			sectorsCount = result.SectorsCount
			sectorsInSkippedPartitions = result.SectorsInSkippedPartitions
			partitionsCount = result.PartitionsCount
			sampledPartitionsCount = result.SampledPartitionsCount
			break loop
		case err := <-errorCh:
			log.Printf("Error at epoch %d: %v", epoch, err)
			return
		case progress := <-progressCh:
			if progress.Epoch > 0 {
				fmt.Printf("Epoch: %d\n", progress.Epoch)
				fmt.Printf("Worker: %v (Balance: %v)\n", progress.MinerInfo.Worker,
					util.ToFIL(progress.WorkerActor.Balance.Int))
				fmt.Printf("Epoch used for immutable deadlines: %d (Worker balance: %v)\n",
					progress.PrevHeightForImmutable,
					util.ToFIL(progress.WorkerActorPrev.Balance.Int))
				fmt.Printf("Batch Size: %d\n", progress.BatchSize)
				fmt.Printf("Gas Limit: %d\n", progress.GasLimit)
			}
			if progress.SectorsCount > 0 && progress.SliceEnd == 0 {
				immutable := ""
				if progress.DeadlineImmutable {
					immutable = " (Immutable)"
				}
				fmt.Printf("%d/%d: Deadline %d%s Partition %d Sectors %d\n",
					progress.DeadlinePartitionIndex+1,
					progress.DeadlinePartitionCount,
					progress.Deadline,
					immutable,
					progress.Partition,
					progress.SectorsCount,
				)
			}
			if progress.SliceCount > 0 {
				fmt.Printf("  Slice: %d to %d (->%d): %d\n", progress.SliceStart,
					progress.SliceEnd-1, progress.SectorsCount-1, progress.SliceCount)
			}
		}
	}

	fmt.Printf("Sectors Terminated: %d\n", sectorsTerminated)
	fmt.Printf("Sectors In Skipped Partitions: %d\n", sectorsInSkippedPartitions)
	fmt.Printf("Sectors Count: %d\n", sectorsCount)
	fmt.Printf("Partitions Count: %d\n", partitionsCount)
	fmt.Printf("Sampled Partitions Count: %d\n", sampledPartitionsCount)

	fmt.Printf("Miner actor balance (attoFIL): %s\n", actor.Balance)
	fmt.Printf("Total burn (attoFIL): %s\n", totalBurn)
	fmt.Printf("Miner actor balance (FIL): %0.03f\n", util.ToFIL(actor.Balance.Int))
	fmt.Printf("Total burn (FIL): %0.03f\n", util.ToFIL(totalBurn))
	difference := new(big.Int).Sub(actor.Balance.Int, totalBurn)
	fmt.Printf("Approximate liquidation value (FIL): %0.03f\n", util.ToFIL(difference))
	diff, _ := util.ToFIL(difference).Float64()
	balance, _ := util.ToFIL(actor.Balance.Int).Float64()
	pct := diff / balance * 100
	fmt.Printf("Approximate recovery percentage: %0.03f%%\n", pct)
	jsonResult := &JSONResult{
		Epoch:             epoch,
		Miner:             minerAddr,
		SectorsTerminated: sectorsTerminated,
		SectorsCount:      sectorsCount,
		Balance:           actor.Balance.Int,
		TotalBurn:         totalBurn,
		LiquidationValue:  difference,
		RecoveryRatio:     diff / balance,
	}
	b, err := json.Marshal(jsonResult)
	if err != nil {
		log.Printf("Error at epoch %d: %v", epoch, err)
		return
	}
	str := fmt.Sprintf("%s\n", string(b))
	io.WriteString(w, str)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func main() {
	viper.AddConfigPath(".")
	viper.SetConfigName("config")
	viper.SetConfigType("env")
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Lotus RPC URL:", viper.GetString("lotus_addr"))
	fmt.Println("Chain ID:", viper.GetString("chain_id"))

	node.InitPoolsArchiveSDK(
		context.Background(),
		viper.GetInt64("chain_id"),
		viper.GetString("lotus_addr"),
		viper.GetString("lotus_token"),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/", getRoot)
	handler := cors.Default().Handler(mux)
	err = http.ListenAndServe(":3000", handler)
	if errors.Is(err, http.ErrServerClosed) {
		fmt.Printf("server closed\n")
	} else if err != nil {
		fmt.Printf("error starting server: %s\n", err)
		os.Exit(1)
	}
}
