package server

import (
	"net"

	"github.com/coreos/etcd/clientv3"
	"github.com/gogo/protobuf/proto"
	. "github.com/pingcap/check"
	"github.com/pingcap/pd/protopb"
)

var _ = Suite(&testClusterSuite{})

type testClusterSuite struct {
	client *clientv3.Client
	svr    *Server
}

func (s *testClusterSuite) getRootPath() string {
	return "test_cluster"
}

func (s *testClusterSuite) SetUpSuite(c *C) {
	s.svr = newTestServer(c, s.getRootPath())

	s.client = newEtcdClient(c)

	deleteRoot(c, s.client, s.getRootPath())

	go s.svr.Run()
}

func (s *testClusterSuite) TearDownSuite(c *C) {
	s.svr.Close()
	s.client.Close()
}

func (s *testClusterSuite) allocID(c *C) uint64 {
	id, err := s.svr.idAlloc.Alloc()
	c.Assert(err, IsNil)
	return id
}

func newRequestHeader(clusterID uint64) *protopb.RequestHeader {
	return &protopb.RequestHeader{
		ClusterId: proto.Uint64(clusterID),
	}
}

func (s *testClusterSuite) newNode(c *C, nodeID uint64, addr string) *protopb.Node {
	if nodeID == 0 {
		nodeID = s.allocID(c)
	}

	return &protopb.Node{
		NodeId:  proto.Uint64(nodeID),
		Address: proto.String(addr),
	}
}

func (s *testClusterSuite) newStore(c *C, nodeID uint64, storeID uint64) *protopb.Store {
	if storeID == 0 {
		storeID = s.allocID(c)
	}

	c.Assert(nodeID, Greater, uint64(0))
	return &protopb.Store{
		NodeId:  proto.Uint64(nodeID),
		StoreId: proto.Uint64(storeID),
	}
}

func (s *testClusterSuite) newPeer(c *C, nodeID uint64, storeID uint64, peerID uint64) *protopb.Peer {
	c.Assert(nodeID, Greater, uint64(0))
	c.Assert(storeID, Greater, uint64(0))

	if peerID == 0 {
		peerID = s.allocID(c)
	}

	return &protopb.Peer{
		NodeId:  proto.Uint64(nodeID),
		StoreId: proto.Uint64(storeID),
		PeerId:  proto.Uint64(peerID),
	}
}

func (s *testClusterSuite) newRegion(c *C, regionID uint64, startKey []byte, endKey []byte, peers []*protopb.Peer) *protopb.Region {
	if regionID == 0 {
		regionID = s.allocID(c)
	}

	maxPeerID := uint64(0)
	for _, peer := range peers {
		peerID := peer.GetPeerId()
		c.Assert(peerID, Greater, uint64(0))
		if peerID > maxPeerID {
			maxPeerID = peerID
		}
	}

	return &protopb.Region{
		RegionId:  proto.Uint64(regionID),
		StartKey:  startKey,
		EndKey:    endKey,
		Peers:     peers,
		MaxPeerId: proto.Uint64(maxPeerID),
	}
}

func (s *testClusterSuite) TestBootstrap(c *C) {
	leader := mustGetLeader(c, s.client, s.getRootPath())

	conn, err := net.Dial("tcp", leader.GetAddr())
	c.Assert(err, IsNil)
	defer conn.Close()

	clusterID := uint64(0)

	// IsBootstrapped returns false.
	req := &protopb.Request{
		Header:         newRequestHeader(clusterID),
		CmdType:        protopb.CommandType_IsBootstrapped.Enum(),
		IsBootstrapped: &protopb.IsBootstrappedRequest{},
	}

	sendRequest(c, conn, 0, req)
	_, resp := recvResponse(c, conn)
	c.Assert(resp.IsBootstrapped, NotNil)
	c.Assert(resp.IsBootstrapped.GetBootstrapped(), IsFalse)

	// Bootstrap the cluster.
	node := s.newNode(c, 0, "127.0.0.1:20163")
	store := s.newStore(c, node.GetNodeId(), 0)
	peer := s.newPeer(c, node.GetNodeId(), store.GetStoreId(), 0)
	region := s.newRegion(c, 0, []byte{}, []byte{}, []*protopb.Peer{peer})
	req = &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_Bootstrap.Enum(),
		Bootstrap: &protopb.BootstrapRequest{
			Node:   node,
			Stores: []*protopb.Store{store},
			Region: region,
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp = recvResponse(c, conn)
	c.Assert(resp.Bootstrap, NotNil)

	// IsBootstrapped returns true.
	req = &protopb.Request{
		Header:         newRequestHeader(clusterID),
		CmdType:        protopb.CommandType_IsBootstrapped.Enum(),
		IsBootstrapped: &protopb.IsBootstrappedRequest{},
	}

	sendRequest(c, conn, 0, req)
	_, resp = recvResponse(c, conn)
	c.Assert(resp.IsBootstrapped, NotNil)
	c.Assert(resp.IsBootstrapped.GetBootstrapped(), IsTrue)

	// check bootstrapped error.
	req = &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_Bootstrap.Enum(),
		Bootstrap: &protopb.BootstrapRequest{
			Node:   node,
			Stores: []*protopb.Store{store},
			Region: region,
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp = recvResponse(c, conn)
	c.Assert(resp.Bootstrap, IsNil)
	c.Assert(resp.Header.Error, NotNil)
	c.Assert(resp.Header.Error.Bootstrapped, NotNil)
}

// helper function to check and bootstrap
func (s *testClusterSuite) bootstrapCluster(c *C, conn net.Conn, clusterID uint64, nodeAddr string) {
	node := s.newNode(c, 0, nodeAddr)
	store := s.newStore(c, node.GetNodeId(), 0)
	peer := s.newPeer(c, node.GetNodeId(), store.GetStoreId(), 0)
	region := s.newRegion(c, 0, []byte{}, []byte{}, []*protopb.Peer{peer})
	req := &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_Bootstrap.Enum(),
		Bootstrap: &protopb.BootstrapRequest{
			Node:   node,
			Stores: []*protopb.Store{store},
			Region: region,
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp := recvResponse(c, conn)
	c.Assert(resp.Bootstrap, NotNil)
}

func (s *testClusterSuite) getNode(c *C, conn net.Conn, clusterID uint64, nodeID uint64) *protopb.Node {
	req := &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_GetMeta.Enum(),
		GetMeta: &protopb.GetMetaRequest{
			MetaType: protopb.MetaType_NodeType.Enum(),
			NodeId:   proto.Uint64(nodeID),
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp := recvResponse(c, conn)
	c.Assert(resp.GetMeta, NotNil)
	c.Assert(resp.GetMeta.GetMetaType(), Equals, protopb.MetaType_NodeType)
	c.Assert(resp.GetMeta.GetNode().GetNodeId(), Equals, uint64(nodeID))

	return resp.GetMeta.GetNode()
}

func (s *testClusterSuite) getStore(c *C, conn net.Conn, clusterID uint64, storeID uint64) *protopb.Store {
	req := &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_GetMeta.Enum(),
		GetMeta: &protopb.GetMetaRequest{
			MetaType: protopb.MetaType_StoreType.Enum(),
			StoreId:  proto.Uint64(storeID),
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp := recvResponse(c, conn)
	c.Assert(resp.GetMeta, NotNil)
	c.Assert(resp.GetMeta.GetMetaType(), Equals, protopb.MetaType_StoreType)
	c.Assert(resp.GetMeta.GetStore().GetStoreId(), Equals, uint64(storeID))
	return resp.GetMeta.GetStore()
}

func (s *testClusterSuite) getRegion(c *C, conn net.Conn, clusterID uint64, regionKey []byte) *protopb.Region {
	req := &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_GetMeta.Enum(),
		GetMeta: &protopb.GetMetaRequest{
			MetaType:  protopb.MetaType_RegionType.Enum(),
			RegionKey: regionKey,
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp := recvResponse(c, conn)
	c.Assert(resp.GetMeta, NotNil)
	c.Assert(resp.GetMeta.GetMetaType(), Equals, protopb.MetaType_RegionType)
	c.Assert(resp.GetMeta.GetRegion(), NotNil)

	return resp.GetMeta.GetRegion()
}

func (s *testClusterSuite) TestGetPutMeta(c *C) {
	leader := mustGetLeader(c, s.client, s.getRootPath())

	conn, err := net.Dial("tcp", leader.GetAddr())
	c.Assert(err, IsNil)
	defer conn.Close()

	clusterID := uint64(1)

	nodeAddr := "127.0.0.1:0"
	s.bootstrapCluster(c, conn, clusterID, nodeAddr)

	// Get region
	region := s.getRegion(c, conn, clusterID, []byte("abc"))
	c.Assert(region.GetPeers(), HasLen, 1)
	peer := region.GetPeers()[0]

	// Get node
	nodeID := peer.GetNodeId()
	node := s.getNode(c, conn, clusterID, nodeID)
	c.Assert(node.GetAddress(), Equals, nodeAddr)

	// Get store
	storeID := peer.GetStoreId()
	store := s.getStore(c, conn, clusterID, storeID)
	c.Assert(store.GetNodeId(), Equals, uint64(nodeID))

	// Update node
	nodeAddr = "127.0.0.1:1"
	req := &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_PutMeta.Enum(),
		PutMeta: &protopb.PutMetaRequest{
			MetaType: protopb.MetaType_NodeType.Enum(),
			Node:     s.newNode(c, nodeID, nodeAddr),
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp := recvResponse(c, conn)
	c.Assert(resp.PutMeta, NotNil)
	c.Assert(resp.PutMeta.GetMetaType(), Equals, protopb.MetaType_NodeType)

	node = s.getNode(c, conn, clusterID, nodeID)
	c.Assert(node.GetAddress(), Equals, nodeAddr)

	// Add another store
	req = &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_PutMeta.Enum(),
		PutMeta: &protopb.PutMetaRequest{
			MetaType: protopb.MetaType_StoreType.Enum(),
			Store:    s.newStore(c, nodeID, 0),
		},
	}
	storeID = req.PutMeta.Store.GetStoreId()
	sendRequest(c, conn, 0, req)
	_, resp = recvResponse(c, conn)
	c.Assert(resp.PutMeta, NotNil)
	c.Assert(resp.PutMeta.GetMetaType(), Equals, protopb.MetaType_StoreType)
	store = s.getStore(c, conn, clusterID, storeID)
	c.Assert(store.GetNodeId(), Equals, uint64(nodeID))

	// Add a new store but we don't add node before, must error
	nodeID = s.allocID(c)
	req = &protopb.Request{
		Header:  newRequestHeader(clusterID),
		CmdType: protopb.CommandType_PutMeta.Enum(),
		PutMeta: &protopb.PutMetaRequest{
			MetaType: protopb.MetaType_StoreType.Enum(),
			Store:    s.newStore(c, nodeID, 0),
		},
	}
	sendRequest(c, conn, 0, req)
	_, resp = recvResponse(c, conn)
	c.Assert(resp.PutMeta, IsNil)
	c.Assert(resp.Header.GetError(), NotNil)
}