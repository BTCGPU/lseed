package main

import (
	"flag"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/cdecker/lseed/lightningrpc"

	log "github.com/Sirupsen/logrus"
	"github.com/cdecker/lseed/seed"
)

var (
	rpcClient lightningrpc.RpcInterface

	listenAddr   = flag.String("listen", "0.0.0.0:53", "Listen address for incoming requests.")
	rootDomain   = flag.String("root-domain", "lseed.bitcoinstats.com", "Root DNS seed domain.")
	pollInterval = flag.Int("poll-interval", 10, "Time between polls to lightningd for updates in second.")
	debug        = flag.Bool("debug", false, "Be very verbose")
	numResults   = flag.Int("results", 25, "How many results shall we return to a query?")

	// Lightning Network client type
	clientType = flag.String("client-type", "lnd", "The lightning client (supported: clightning, lnd)")

	// Flags for c-lightning
	lightningSock = flag.String("lightning-sock", "~/.lightning/lightning-rpc", "Location of the lightning socket")

	// Flags for lnd
	lndRpcServer       = flag.String("rpcserver", "localhost:10009", "host:port of ln daemon")
	lndDirPath         = flag.String("lnddir", "~/.lnd", "Path to lnd's base directory")
	lndTlsCertPath     = flag.String("tlscertpath", "~/.lnd/tls.cert", "Path to TLS certificate")
	lndNoMacaroons     = flag.Bool("no-macaroons", false, "Skip Macaroon authetication for lnd")
	lndMacaroonPath    = flag.String("macaroonpath", "~/.lnd/admin.macaroon", "Path to macaroon file")
	lndMacaroonTimeout = flag.Int64("macaroontimeout", 0, "Anti-replay macaroon validity time in seconds")
	lndMacaroonIp      = flag.String("macaroonip", "", "If set, lock macaroon to specific IP address")
)

// cleanAndExpandPath expands environment variables and leading ~ in the
// passed path, cleans the result, and returns it.
// This function is taken from https://github.com/btcsuite/btcd
func cleanAndExpandPath(path string) string {
	// Expand initial ~ to OS specific home directory.
	if strings.HasPrefix(path, "~") {
		var homeDir string

		user, err := user.Current()
		if err == nil {
			homeDir = user.HomeDir
		} else {
			homeDir = os.Getenv("HOME")
		}

		path = strings.Replace(path, "~", homeDir, 1)
		path = strings.Replace(path, "$HOME", homeDir, 1)
	}

	// NOTE: The os.ExpandEnv doesn't work with Windows-style %VARIABLE%,
	// but the variables can still be expanded via POSIX-style $VARIABLE.
	return filepath.Clean(os.ExpandEnv(path))
}

// Expand variables in paths such as $HOME
func expandAndInitVariables() {
	switch *clientType {
	case "clightning":
		*lightningSock = cleanAndExpandPath(*lightningSock)
		rpcClient = lightningrpc.NewLightningRpc(*lightningSock)
	case "lnd":
		*lndDirPath = cleanAndExpandPath(*lndDirPath)
		*lndTlsCertPath = cleanAndExpandPath(*lndTlsCertPath)
		*lndMacaroonPath = cleanAndExpandPath(*lndMacaroonPath)
		rpcClient = lightningrpc.NewLndRpc(*lndTlsCertPath, *lndNoMacaroons, *lndMacaroonTimeout, *lndMacaroonIp, *lndMacaroonPath, *lndRpcServer)
	default:
		log.Errorf("ERROR: Unrecognized client type")
	}
}

// Regularly polls the lightningd node and updates the local NetworkView.
func poller(lrpc lightningrpc.RpcInterface, nview *seed.NetworkView) {
	scrapeGraph := func() {
		r, err := lrpc.ListNodes()

		if err != nil {
			log.Errorf("Error trying to get update from lightningd: %v", err)
		} else {
			log.Debugf("Got %d nodes from lightningd", len(r.Nodes))
			for _, n := range r.Nodes {
				if len(n.Addresses) == 0 {
					continue
				}
				nview.AddNode(n)
			}
		}
	}

	scrapeGraph()

	ticker := time.NewTicker(time.Second * time.Duration(*pollInterval))
	for range ticker.C {
		scrapeGraph()
	}
}

// Parse flags and configure subsystems according to flags
func configure() {
	flag.Parse()
	expandAndInitVariables()
	if *debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
}

// Main entry point for the lightning-seed
func main() {
	configure()

	nview := seed.NewNetworkView()
	dnsServer := seed.NewDnsServer(nview, *listenAddr, *rootDomain)

	go poller(rpcClient, nview)
	dnsServer.Serve()
}
