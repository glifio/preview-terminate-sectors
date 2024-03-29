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
	"github.com/filecoin-project/lotus/api"
	filtypes "github.com/filecoin-project/lotus/chain/types"
	"github.com/glifio/go-pools/mstat"
	"github.com/glifio/go-pools/terminate"
	"github.com/glifio/go-pools/util"
	"github.com/jimpick/preview-terminate-sectors/node"
	"github.com/rs/cors"
	"github.com/spf13/viper"
)

// var height uint64 = 3461984

type JSONResult struct {
	Epoch             uint64
	Miner             address.Address
	MinerStats        *mstat.MinerStats
	SectorsTerminated uint64
	SectorsCount      uint64
	Balance           *big.Int
	LiquidationValue  *big.Int
	SectorStats       *terminate.SectorStats
	RecoveryRatio     float64
	MinerPower        *api.MinerPower
	AverageAge        uint64
	Error             string
}

var pathRE *regexp.Regexp

func init() {
	pathRE = regexp.MustCompile(`^/([ft]0\d+)$`)
}

func auth(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != viper.GetString("auth_user") || pass != viper.GetString("auth_password") {
			http.Error(w, "Unauthorized.", http.StatusUnauthorized)
			return
		}
		fn(w, r)
	}
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != "GET" {
		str := fmt.Sprintf("Unsupported Method: %s", r.Method)
		log.Print(str)
		http.Error(w, str, http.StatusNotFound)
		return
	}

	fmt.Println("---")
	fmt.Printf("Request Path: %+v\n", r.URL.Path)
	fmt.Printf("Query: %+v\n", r.URL.Query())

	match := pathRE.FindStringSubmatch(r.URL.Path)
	if len(match) != 2 {
		str := fmt.Sprintf("Miner ID not found in URL path: %s", r.URL.Path)
		log.Print(str)
		http.Error(w, str, http.StatusNotFound)
		return
	}
	minerID := match[1]

	client, closer, err := node.PoolsArchiveSDK.Extern().ConnectLotusClient()
	if err != nil {
		log.Fatal(err)
	}
	defer closer()

	var batchSize uint64 = 40
	var gasLimit uint64 = 270000000000
	var maxPartitions uint64 = 21

	sampleSectors := true
	optimize := true
	offchain := true

	var epochs []uint64
	if r.URL.Query().Has("epochs") {
		epochsString := r.URL.Query().Get("epochs")
		epochsStrings := strings.Split(epochsString, ",")
		for _, epochString := range epochsStrings {
			epochNum, err := strconv.ParseUint(epochString, 10, 64)
			if err != nil {
				str := fmt.Sprintf("Could not parse epoch: %s", epochString)
				log.Print(str)
				http.Error(w, str, http.StatusBadRequest)
				return
			}
			epochs = append(epochs, epochNum)
		}
	}

	minerAddr, err := address.NewFromString(minerID)
	if err != nil {
		str := fmt.Sprintf("Could not parse miner ID: %s", minerID)
		log.Print(str)
		http.Error(w, str, http.StatusBadRequest)
		return
	}

	if len(epochs) == 0 {
		epochs = append(epochs, 0)
	}

	var ts *filtypes.TipSet

epochsLoop:
	for i, height := range epochs {
		fmt.Printf(">>> %d/%d: Height %d\n", i+1, len(epochs), height)
		tipset := ""
		if height > 0 {
			tipset = fmt.Sprintf("@%d", height)
		}

		errorCh := make(chan error)
		progressCh := make(chan *terminate.PreviewTerminateSectorsProgress)
		resultCh := make(chan *terminate.PreviewTerminateSectorsReturn)

		go terminate.PreviewTerminateSectors(ctx, *client, minerAddr,
			tipset, height, batchSize, gasLimit, sampleSectors, optimize, offchain,
			maxPartitions, errorCh, progressCh, resultCh)

		var epoch uint64 = height
		var actor *filtypes.ActorV5
		var stats *terminate.SectorStats
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
				stats = result.SectorStats
				sectorsTerminated = result.SectorsTerminated
				sectorsCount = result.SectorsCount
				sectorsInSkippedPartitions = result.SectorsInSkippedPartitions
				partitionsCount = result.PartitionsCount
				sampledPartitionsCount = result.SampledPartitionsCount
				ts = result.Tipset
				break loop
			case err := <-errorCh:
				log.Printf("Error at epoch %d: %v", epoch, err)
				jsonResult := &JSONResult{
					Epoch: epoch,
					Error: err.Error(),
				}
				b, _ := json.Marshal(jsonResult)
				io.WriteString(w, string(b)+"\n")
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
		fmt.Printf("Total burn (attoFIL): %s\n", stats.TerminationPenalty)
		fmt.Printf("Miner actor balance (FIL): %0.03f\n", util.ToFIL(actor.Balance.Int))
		fmt.Printf("Total burn (FIL): %0.03f\n", util.ToFIL(stats.TerminationPenalty))
		difference := new(big.Int).Sub(actor.Balance.Int, stats.TerminationPenalty)
		fmt.Printf("Approximate liquidation value (FIL): %0.03f\n", util.ToFIL(difference))
		diff, _ := util.ToFIL(difference).Float64()
		balance, _ := util.ToFIL(actor.Balance.Int).Float64()
		pct := diff / balance * 100
		fmt.Printf("Approximate recovery percentage: %0.03f%%\n", pct)

		minerPower, err := client.StateMinerPower(ctx, minerAddr, ts.Key())
		if err != nil {
			log.Printf("Error at epoch %d: %v", epoch, err)
			jsonResult := &JSONResult{
				Epoch: epoch,
				Error: err.Error(),
			}
			b, _ := json.Marshal(jsonResult)
			io.WriteString(w, string(b)+"\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			continue epochsLoop
		}

		minerStats, err := mstat.ComputeMinerStats(ctx, minerAddr, ts, client)
		if err != nil {
			log.Printf("Error at epoch %d: %v", epoch, err)
			jsonResult := &JSONResult{
				Epoch: epoch,
				Error: err.Error(),
			}
			b, _ := json.Marshal(jsonResult)
			io.WriteString(w, string(b)+"\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			continue epochsLoop
		}

		var ageAvg uint64
		if stats.SectorCount > 0 {
			ageAvg = new(big.Int).Div(stats.Age, big.NewInt(stats.SectorCount)).Uint64()
		}

		jsonResult := &JSONResult{
			Epoch:             epoch,
			Miner:             minerAddr,
			MinerStats:        minerStats,
			SectorsTerminated: sectorsTerminated,
			SectorsCount:      sectorsCount,
			Balance:           actor.Balance.Int,
			SectorStats:       stats,
			LiquidationValue:  difference,
			RecoveryRatio:     diff / balance,
			MinerPower:        minerPower,
			AverageAge:        ageAvg,
		}
		b, err := json.Marshal(jsonResult)
		if err != nil {
			log.Printf("Error at epoch %d: %v", epoch, err)
			jsonResult := &JSONResult{
				Epoch: epoch,
				Error: err.Error(),
			}
			b, _ = json.Marshal(jsonResult)
		}
		io.WriteString(w, string(b)+"\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	fmt.Println("Done.")
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
	mux.HandleFunc("/", auth(getRoot))
	corsOrigins := viper.GetString("cors_origins")

	var handler http.Handler
	if corsOrigins != "" {
		fmt.Println("Using CORS origins:", corsOrigins)
		origins := strings.Split(corsOrigins, ",")
		c := cors.New(cors.Options{
			AllowedOrigins:   origins,
			AllowCredentials: true,
			AllowedHeaders:   []string{"Authorization"},
			// Enable Debugging for testing, consider disabling in production
			Debug: true,
		})
		handler = c.Handler(mux)
	} else {
		handler = mux
	}
	err := http.ListenAndServe(":3000", handler)
	if errors.Is(err, http.ErrServerClosed) {
		fmt.Printf("server closed\n")
	} else if err != nil {
		fmt.Printf("error starting server: %s\n", err)
		os.Exit(1)
	}
}
