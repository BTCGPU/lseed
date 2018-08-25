package lightningrpc

import (
	"io/ioutil"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/lightningnetwork/lnd/lncfg"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/macaroons"

	macaroon "gopkg.in/macaroon.v2"

	"io"
	"strconv"
	"strings"

	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
)

type LndRpc struct {
	tlsCertPath     string
	noMacaroons     bool
	macaroonTimeout int64
	macaroonIp      string
	macaroonPath    string
	rpcServer       string
	client          lnrpc.LightningClient
	clientCleanup   func()
	graphCleanup    chan struct{}
	ctx             context.Context
}

func IsIPv4(address string) bool {
	return strings.Count(address, ":") == 0
}

func IsIPv6(address string) bool {
	return strings.Count(address, ":") >= 2
}

func NewLndRpc(tlsCertPath string, noMacaroons bool, macaroonTimeout int64, macaroonIp string, macaroonPath string, rpcServer string) *LndRpc {
	lnd := LndRpc{
		tlsCertPath:     tlsCertPath,
		noMacaroons:     noMacaroons,
		macaroonTimeout: macaroonTimeout,
		macaroonIp:      macaroonIp,
		macaroonPath:    macaroonPath,
		rpcServer:       rpcServer,
		ctx:             context.Background(),
	}

	lnd._initClient()

	return &lnd
}

func (lnd *LndRpc) ListNodes() (ListNodesResponse, error) {
	// Aquire rpc client
	client := lnd.getClient()

	// Init response
	res := ListNodesResponse{}

	// Get node graph
	graphRes, err := client.DescribeGraph(lnd.ctx, &lnrpc.ChannelGraphRequest{})

	if err == nil {
		for _, n := range graphRes.Nodes {
			niRes, err := client.GetNodeInfo(lnd.ctx, &lnrpc.NodeInfoRequest{PubKey: n.PubKey})

			if err != nil {
				log.Errorf("%v", err)
			} else {

				// log.Debugf("%v", niRes)

				// There is no point in doing the work without at least one address
				if len(niRes.Node.Addresses) > 0 {
					// Create node object and fill it
					node := Node{
						Id:         niRes.Node.PubKey,
						Alias:      niRes.Node.Alias,
						Color:      niRes.Node.Color,
						LastUpdate: niRes.Node.LastUpdate,
					}

					// Iterate and parse addresses
					for _, a := range niRes.Node.Addresses {
						ip, ports, err := net.SplitHostPort(a.Addr)

						if err != nil {
							continue
						}

						ipType := ""

						if IsIPv4(ip) {
							ipType = "ipv4"
						} else if IsIPv6(ip) {
							ipType = "ipv6"
						} else {
							continue
						}

						port, err := strconv.ParseUint(ports, 10, 16)

						if err == nil {
							node.Addresses = append(node.Addresses, NodeAddress{
								Address: ip,
								Type:    ipType,
								Port:    uint16(port),
							})
						}
					}

					// Add node only if at least one address could be created/parsed
					if len(node.Addresses) > 0 {
						res.Nodes = append(res.Nodes, node)
					}
				}
			}
		}

		log.Debugf("%v", res.Nodes)
	} else {
		// Cleanup
		lnd.shutdown()
	}

	return res, err
}

func (lnd *LndRpc) getClient() lnrpc.LightningClient {
	if lnd.client == nil {
		lnd._initClient()
	}

	return lnd.client
}

func (lnd *LndRpc) _initClient() {
	// Init rpc client
	lnd.client, lnd.clientCleanup = lnd._getClient()

	// Leaving this here for possible improvement to go away from polling as notifications have less overhead
	/*
		graphUpdates, graphCleanup := lnd.subscribeGraphNotifications()
		lnd.graphCleanup = graphCleanup

		updates := func(graphUpdates <-chan *lnrpc.GraphTopologyUpdate) {
			for {
				select {
				case graphUpdate := <-graphUpdates:
					for _, update := range graphUpdate.NodeUpdates {
						log.Debugf("%v", update.Addresses)
					}
				}
			}
		}
		go updates(graphUpdates)
	*/
}

func (lnd *LndRpc) _getClient() (lnrpc.LightningClient, func()) {
	conn := lnd._getClientConn(false)

	cleanUp := func() {
		conn.Close()
	}

	return lnrpc.NewLightningClient(conn), cleanUp
}

func (lnd *LndRpc) _getClientConn(skipMacaroons bool) *grpc.ClientConn {
	// Load the specified TLS certificate and build transport credentials
	// with it.

	creds, err := credentials.NewClientTLSFromFile(lnd.tlsCertPath, "")
	if err != nil {
		log.Errorf("%v", err)
	}

	// Create a dial options array.
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}

	// Only process macaroon credentials if --no-macaroons isn't set and
	// if we're not skipping macaroon processing.
	if !lnd.noMacaroons && !skipMacaroons {
		// Load the specified macaroon file.
		macBytes, err := ioutil.ReadFile(lnd.macaroonPath)
		if err != nil {
			log.Errorf("%v", err)
		}
		mac := &macaroon.Macaroon{}
		if err = mac.UnmarshalBinary(macBytes); err != nil {
			log.Errorf("%v", err)
		}

		var macConstraints []macaroons.Constraint

		// Optionally add timeout.
		if lnd.macaroonTimeout > 0 {
			macConstraints = append(macConstraints,
				macaroons.TimeoutConstraint(lnd.macaroonTimeout))
		}

		// Lock macaroon down to a specific IP address.
		macConstraints = append(macConstraints, macaroons.IPLockConstraint(lnd.macaroonIp))
		// ... Add more constraints if needed.

		// Apply constraints to the macaroon.
		constrainedMac, err := macaroons.AddConstraints(mac, macConstraints...)
		if err != nil {
			log.Errorf("%v", err)
		}

		// Now we append the macaroon credentials to the dial options.
		cred := macaroons.NewMacaroonCredential(constrainedMac)
		opts = append(opts, grpc.WithPerRPCCredentials(cred))
	}

	// We need to use a custom dialer so we can also connect to unix sockets
	// and not just TCP addresses.
	opts = append(
		opts, grpc.WithDialer(
			lncfg.ClientAddressDialer("10009"),
		),
	)
	conn, err := grpc.Dial(lnd.rpcServer, opts...)
	if err != nil {
		log.Errorf("%v", err)
	}

	return conn
}

func (lnd *LndRpc) subscribeGraphNotifications() (chan *lnrpc.GraphTopologyUpdate, chan struct{}) {
	// We'll first start by establishing a notification client which will
	// send us notifications upon detected changes in the channel graph.
	req := &lnrpc.GraphTopologySubscription{}
	ctx, cancelFunc := context.WithCancel(context.Background())
	topologyClient, err := lnd.client.SubscribeChannelGraph(ctx, req)

	if err != nil {
		log.Fatalf("unable to create topology client: %v", err)
	}

	// We'll launch a goroutine that will be responsible for proxying all
	// notifications recv'd from the client into the channel below.
	quit := make(chan struct{})
	graphUpdates := make(chan *lnrpc.GraphTopologyUpdate, 20)
	go func() {
		for {
			defer cancelFunc()

			select {
			case <-quit:
				return
			default:
				graphUpdate, err := topologyClient.Recv()
				select {
				case <-quit:
					return
				default:
				}

				if err == io.EOF {
					return
				} else if err != nil {
					log.Errorf("unable to recv graph update: %v", err)
				}

				select {
				case graphUpdates <- graphUpdate:
				case <-quit:
					return
				}
			}
		}
	}()
	return graphUpdates, quit
}

func (lnd *LndRpc) shutdown() {
	// Stop graph notification goroutine
	if lnd.graphCleanup != nil {
		close(lnd.graphCleanup)
	}

	// Cleanup rpc client
	if lnd.clientCleanup != nil {
		lnd.clientCleanup()
		lnd.clientCleanup = nil
		lnd.client = nil
	}
}
