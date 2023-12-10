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
	"regexp"
	"strconv"
	"strings"

	"github.com/filecoin-project/go-address"
	filtypes "github.com/filecoin-project/lotus/chain/types"
	"github.com/glifio/go-pools/types"
	"github.com/glifio/go-pools/util"
	"github.com/jimpick/preview-terminate-sectors/node"
	"github.com/rs/cors"
	"github.com/spf13/viper"
)

// var height uint64 = 3461984

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

var pathRE *regexp.Regexp

func init() {
	pathRE = regexp.MustCompile(`^/([ft]0\d+)$`)
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	fmt.Println("---")
	fmt.Printf("Request Path: %+v\n", r.URL.Path)

	match := pathRE.FindStringSubmatch(r.URL.Path)
	if len(match) != 2 {
		str := fmt.Sprintf("Miner ID not found in URL path: %s", r.URL.Path)
		log.Print(str)
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, str)
		io.WriteString(w, "\n")
		return
	}
	minerID := match[1]

	query := node.PoolsArchiveSDK.Query()

	var batchSize uint64 = 40
	var gasLimit uint64 = 270000000000
	var maxPartitions uint64 = 21

	sampleSectors := true
	optimize := true
	offchain := true

	var epochs []uint64

	fmt.Printf("Query: %+v\n", r.URL.Query())
	if r.URL.Query().Has("epochs") {
		epochsString := r.URL.Query().Get("epochs")
		epochsStrings := strings.Split(epochsString, ",")
		for _, epochString := range epochsStrings {
			epochNum, err := strconv.ParseUint(epochString, 10, 64)
			if err != nil {
				str := fmt.Sprintf("Could not parse epoch: %s", epochString)
				log.Print(str)
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, str)
				io.WriteString(w, "\n")
				return
			}
			epochs = append(epochs, epochNum)
		}
	}

	minerAddr, err := address.NewFromString(minerID)
	if err != nil {
		str := fmt.Sprintf("Could not parse miner ID: %s", minerID)
		log.Print(str)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, str)
		io.WriteString(w, "\n")
		return
	}

	if len(epochs) == 0 {
		epochs = append(epochs, 0)
	}

epochsLoop:
	for _, height := range epochs {
		tipset := ""
		if height > 0 {
			tipset = fmt.Sprintf("@%d", height)
		}

		errorCh := make(chan error)
		progressCh := make(chan *types.PreviewTerminateSectorsProgress)
		resultCh := make(chan *types.PreviewTerminateSectorsReturn)

		go query.PreviewTerminateSectors(ctx, minerAddr,
			tipset, height, batchSize, gasLimit, sampleSectors, optimize, offchain,
			maxPartitions, errorCh, progressCh, resultCh)

		var epoch uint64 = height
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
				epoch = uint64(result.Epoch)
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
				str := fmt.Sprintf("{\"Epoch\": %d, \"Error\": \"%s\"}\n", epoch, err)
				io.WriteString(w, str)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				continue epochsLoop
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
			b = []byte(fmt.Sprintf("{\"Epoch\": %d, \"Error\": \"%s\"}", epoch, err))
		}
		str := fmt.Sprintf("%s\n", string(b))
		io.WriteString(w, str)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func main() {
	viper.AddConfigPath(".")
	viper.SetConfigName("config")
	viper.SetConfigType("env")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found; ignore error if desired
			log.Printf("Warning: %v\n", err)
		} else {
			// Config file was found but another error was produced
			log.Fatal(err)
		}
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
	err := http.ListenAndServe(":3000", handler)
	if errors.Is(err, http.ErrServerClosed) {
		fmt.Printf("server closed\n")
	} else if err != nil {
		fmt.Printf("error starting server: %s\n", err)
		os.Exit(1)
	}
}
