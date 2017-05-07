package simulations

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/simulations/adapters"
)

type MockerConfig struct {
	Id              string
	NodeCount       int
	UpdateInterval  int
	SwitchonRate    int // fraction of off nodes switching on
	DropoutRate     int // fraction of on nodes dropping out
	NewConnCount    int // new connection per node per tick
	ConnFailRate    int // fraction of connections failing
	DisconnRate     int // fraction of all connections
	NodesTarget     int // total number of nodes to converge on
	DegreeTarget    int // number of connections per peer to converge on
	ConvergenceRate int // speed of convergence
	ticker          *time.Ticker
}

func DefaultMockerConfig() *MockerConfig {
	return &MockerConfig{
		Id:              "0",
		NodeCount:       100,
		UpdateInterval:  1000,
		SwitchonRate:    5,
		DropoutRate:     100,
		NewConnCount:    1, // new connection per node per tick
		ConnFailRate:    100,
		DisconnRate:     100, // fraction of all connections
		NodesTarget:     50,
		DegreeTarget:    8,
		ConvergenceRate: 5,
	}
}

// base unit is the fixed minimal interval  between two measurements (time quantum)
// acceleration : to slow down you just set the base unit higher.
// to speed up: skip x number of base units
// frequency: given as the (constant or average) number of base units between measurements
// if resolution is expressed as the inverse of frequency  = preserved information
// setting the acceleration
// beginning of the record (lifespan) of the network is index 0
// acceleration means that snapshots are rarer so the same span can be generated by the journal
// then update logs can be compressed (to only one state transition per affected node)
// epoch, epochcount

// MockEvents generates random connectivity events and posts them
// to the eventer
// The journal using the eventer can then be read to visualise or
// drive connections
func MockEvents(eventer *event.Feed, ids []*adapters.NodeId, conf *MockerConfig) {

	var onNodes []*Node
	offNodes := ids
	onConnsMap := make(map[string]int)
	var onConns []*Conn
	connsMap := make(map[string]int)
	var conns []*Conn

	conf.ticker = time.NewTicker(time.Duration(conf.UpdateInterval) * time.Millisecond)
	switchonRate := conf.SwitchonRate
	dropoutRate := conf.DropoutRate
	newConnCount := conf.NewConnCount
	connFailRate := conf.ConnFailRate
	disconnRate := conf.DisconnRate
	nodesTarget := conf.NodesTarget
	degreeTarget := conf.DegreeTarget
	convergenceRate := conf.ConvergenceRate

	rounds := 0
	for _ = range conf.ticker.C {
		log.Trace(fmt.Sprintf("rates: %v/%v, %v (%v/%v)", switchonRate, dropoutRate, newConnCount, connFailRate, disconnRate))
		// here switchon rate will depend
		nodesUp := len(offNodes) / switchonRate
		missing := nodesTarget - len(onNodes)
		if missing > 0 {
			if nodesUp < missing {
				nodesUp += (missing-nodesUp)/convergenceRate + 1
			}
		}

		nodesDown := len(onNodes) / dropoutRate

		connsUp := len(onNodes) * newConnCount
		connsUp = connsUp - connsUp/connFailRate
		missing = nodesTarget*degreeTarget/2 - len(onConns)
		if missing < connsUp {
			connsUp = missing
			if connsUp < 0 {
				connsUp = 0
			}
		}
		connsDown := len(onConns) / disconnRate
		log.Trace(fmt.Sprintf("Nodes Up: %v, Down: %v [ON: %v/%v]\nConns Up: %v, Down: %v [ON: %v/%v(%v)]", nodesUp, nodesDown, len(onNodes), len(onNodes)+len(offNodes), connsUp, connsDown, len(onConns), len(conns)-len(onConns), len(conns)))

		for i := 0; len(onNodes) > 0 && i < nodesDown; i++ {
			c := rand.Intn(len(onNodes))
			sn := onNodes[c]
			eventer.Send(ControlEvent(sn))
			onNodes = append(onNodes[0:c], onNodes[c+1:]...)
			offNodes = append(offNodes, sn.ID())
		}
		var mustconnect []int
		for i := 0; len(offNodes) > 0 && i < nodesUp; i++ {
			c := rand.Intn(len(offNodes))
			sn := &Node{Config: &adapters.NodeConfig{Id: offNodes[c]}}
			eventer.Send(ControlEvent(sn))
			mustconnect = append(mustconnect, len(onNodes))
			onNodes = append(onNodes, sn)
			offNodes = append(offNodes[0:c], offNodes[c+1:]...)
		}
		var found bool
		var sc *Conn
		if connsUp < len(mustconnect) {
			connsUp = len(mustconnect)
		}
		connected := make(map[int]bool)
		for i := 0; len(onNodes) > 1 && i < connsUp; i++ {
			sc = nil
			var n int
			if i < len(mustconnect) {
				n = mustconnect[i]
			} else {
				n = rand.Intn(len(onNodes) - 1)
				if connected[n] {
					continue
				}
			}
			m := n + rand.Intn(len(onNodes)-n)
			// m := n + 1 + rand.Intn(len(onNodes)-n-1)
			for k := m; k < len(onNodes); k++ {
				lab := ConnLabel(onNodes[n].ID(), onNodes[k].ID())
				var j int
				j, found = onConnsMap[lab]
				if found {
					continue
				}
				j, found = connsMap[lab]
				if found {
					sc = conns[j]
					break
				}
				connected[k] = true
				caller := onNodes[n].ID()
				callee := onNodes[k].ID()

				sc := &Conn{
					One:   caller,
					Other: callee,
				}
				connsMap[lab] = len(conns)
				conns = append(conns, sc)
				break
			}

			if sc == nil {
				i--
				continue
			}
			lab := ConnLabel(sc.One, sc.Other)
			onConnsMap[lab] = len(onConns)
			onConns = append(onConns, sc)
			eventer.Send(ControlEvent(sc))
		}

		for i := 0; len(onConns) > 0 && i < connsDown; i++ {
			c := rand.Intn(len(onConns))
			conn := onConns[c]
			onConns = append(onConns[0:c], onConns[c+1:]...)
			lab := ConnLabel(conn.One, conn.Other)
			delete(onConnsMap, lab)
			eventer.Send(ControlEvent(conn))
		}
		rounds++
	}
}

func RandomNodeId() *adapters.NodeId {
	key, err := crypto.GenerateKey()
	if err != nil {
		panic("unable to generate key")
	}
	pubkey := crypto.FromECDSAPub(&key.PublicKey)
	return adapters.NewNodeId(pubkey[1:])
}

func RandomNodeIds(n int) []*adapters.NodeId {
	var ids []*adapters.NodeId
	for i := 0; i < n; i++ {
		ids = append(ids, RandomNodeId())
	}
	return ids
}
