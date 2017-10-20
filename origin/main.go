package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	xconfig "code.uber.internal/go-common.git/x/config"

	"code.uber.internal/go-common.git/x/log"
	"code.uber.internal/infra/kraken/lib/peercontext"
	"code.uber.internal/infra/kraken/lib/store"
	"code.uber.internal/infra/kraken/lib/torrent"
	"code.uber.internal/infra/kraken/metrics"
	"code.uber.internal/infra/kraken/origin/blobclient"
	"code.uber.internal/infra/kraken/origin/blobserver"
)

func main() {
	blobServerPort := flag.Int("blobserver_port", 0, "port which registry listens on")
	peerIP := flag.String("peer_ip", "", "ip which peer will announce itself as")
	peerPort := flag.Int("peer_port", 0, "port which peer will announce itself as")
	flag.Parse()

	if blobServerPort == nil || *blobServerPort == 0 {
		panic("0 is not a valid port for blob server")
	}

	var config Config
	if err := xconfig.Load(&config); err != nil {
		panic(err)
	}

	// Disable JSON logging because it's completely unreadable.
	formatter := true
	config.Logging.TextFormatter = &formatter
	log.Configure(&config.Logging, false)

	// Stats
	stats, closer, err := metrics.New(config.Metrics)
	if err != nil {
		log.Fatalf("Failed to init metrics: %s", err)
	}
	defer closer.Close()

	// root metrics scope for origin
	stats = stats.SubScope("kraken.origin")

	// Initialize file storage
	fileStore, err := store.NewLocalFileStore(&config.LocalStore, true)
	if err != nil {
		log.Fatalf("Failed to create local store: %s", err)
	}

	// Initialize and start P2P scheduler client:
	var torrentClient torrent.Client
	if !config.Torrent.Disabled {
		pctx, err := peercontext.New(
			peercontext.PeerIDFactory(config.Torrent.PeerIDFactory), *peerIP, *peerPort)
		if err != nil {
			log.Fatalf("Failed to create peer context: %s", err)
		}

		torrentClient, err = torrent.NewSchedulerClient(&config.Torrent, fileStore, stats, pctx)
		if err != nil {
			log.Fatalf("Failed to create scheduler client: %s", err)
			panic(err)
		}
	}

	// The code below starts Blob HTTP server.
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("Error getting hostname: %s", err)
	}

	addr := fmt.Sprintf("%s:%d", hostname, *blobServerPort)
	blobClientProvider := blobclient.NewProvider(config.BlobClient)

	stats, closer, err = metrics.New(config.Metrics)
	if err != nil {
		log.Fatalf("Failed to create metrics: %s", err)
	}
	defer closer.Close()

	server, err := blobserver.New(
		config.BlobServer,
		config.Torrent,
		stats,
		addr,
		fileStore,
		blobClientProvider,
		torrentClient)
	if err != nil {
		log.Fatalf("Error initializing blob server %s: %s", addr, err)
	}

	log.Infof("Starting origin server %s on %d", hostname, *blobServerPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *blobServerPort), server.Handler()))
}
