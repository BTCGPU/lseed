package lightningrpc

type NodeAddress struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}

type Node struct {
	Id         string        `json:"nodeid"`
	Addresses  []NodeAddress `json:"addresses"`
	Color      string        `json:"color"`
	Alias      string        `json:"alias"`
	LastUpdate uint32        `json:"last_timestamp"`
}

type ListNodesResponse struct {
	Nodes []Node `json:"nodes"`
}

type RpcInterface interface {
	ListNodes() (ListNodesResponse, error)
}
