package node

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"runtime/pprof"
	"sync"
	"time"

	"example.com/rustsha256/lib"
	logging "github.com/ipfs/go-log/v2"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"golang.org/x/exp/mmap"
	"golang.org/x/xerrors"
)

const HEADCOUNT = 2
const PARENTCOUNTBASE = 6
const PARENTCOUNTEXP = 8
const PARENTCOUNT = PARENTCOUNTBASE + PARENTCOUNTEXP
const NODESIZE = 32
const timeTemplate = "2006-01-02 15:04:05"

var log = logging.Logger("node")
var cachedb = &CacheDB{}

func init() {
	logging.SetLogLevel("node", "INFO")
	if _, found := os.LookupEnv("NODEDEBUG"); found {
		logging.SetLogLevel("node", "DEBUG")
	}
}

type CacheDB struct {
	lk             sync.RWMutex
	CacheSize      uint64
	Cache          map[uint64][]byte
	LastLayerCache []map[string][]byte
	Db             *leveldb.DB
	CurNode        uint64
}

func (db *CacheDB) Get(key []byte, node uint64, ro *opt.ReadOptions, lastLayer bool) ([]byte, error) {
	db.lk.RLock()
	defer db.lk.RUnlock()
	if !lastLayer {
		if node > db.CurNode {
			return nil, xerrors.Errorf("node %v not ready", node)
		}
		if int64(db.CurNode)-int64(db.CacheSize) < int64(node) {
			remainder := node % db.CacheSize
			v := db.Cache[remainder]
			return v, nil
		} else {
			return db.Db.Get(key, ro)
		}
	} else {
		remainder := node % db.CacheSize
		tmpcache := db.LastLayerCache[remainder]
		v, ok := tmpcache[string(key)]
		if ok {
			return v, nil
		}
		v, err := db.Db.Get(key, ro)
		if err != nil {
			return nil, err
		}
		tmpcache = map[string][]byte{string(key): v}
		db.LastLayerCache[remainder] = tmpcache
		return v, err
	}
}

func (db *CacheDB) Put(key []byte, value []byte, node uint64, wo *opt.WriteOptions) error {
	remainder := node % db.CacheSize
	db.lk.Lock()
	defer db.lk.Unlock()
	db.CurNode = node
	db.Cache[remainder] = value
	return db.Db.Put(key, value, wo)
	// return nil
}

type NodeIndex struct {
	Counter int
	Base    bool
	Data    [NODESIZE]byte
}

type Node struct {
	Layer            uint32
	NodeIndex        uint64
	ParentsIndex     []uint64
	ParentsCacheBase map[uint64]*NodeIndex
	ParentsCacheEXP  map[uint64]*NodeIndex
	ExpOk            bool
	ReadyNode        int
	Pub              chan Node
	Recv             chan Node
	Hash             [NODESIZE]byte
	Pmc              *ParentsMapCache
	Db               *CacheDB
	LastNode         bool
	ScheChan         chan interface{}
	WriteChan        chan interface{}
}

func (n *Node) Start(providerID []byte, sectorID []byte, ticket []byte, commd []byte) {
	var state [8]uint32
	porepseed := []byte{99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99}
	sha := sha256.New()
	sha.Write(providerID)
	sha.Write(sectorID)
	sha.Write(ticket)
	sha.Write(commd)
	sha.Write(porepseed)
	replicaID := sha.Sum(nil)
	replicaID[31] &= 0b0011_1111
	var block0 [NODESIZE]byte
	copy(block0[:], replicaID[:])
	var block1 [NODESIZE]byte
	lbuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lbuf, n.Layer)
	nbuf := make([]byte, 8)
	binary.BigEndian.PutUint64(nbuf, n.NodeIndex)
	copy(block1[:4], lbuf[:])
	copy(block1[4:12], nbuf[:])
	var nodehash [NODESIZE]byte
	state = lib.GoShaLayer(state, HEADCOUNT, [PARENTCOUNT][NODESIZE]byte{block0, block1})
	if n.NodeIndex == 0 {
		nodehash = lib.GoFinish(state, uint64(HEADCOUNT<<8))
	} else {
		tmpParentCacheBase := make(map[uint64]interface{})
		for k, _ := range n.ParentsCacheBase {
			tmpParentCacheBase[k] = nil
		}

		tc := time.NewTicker(2 * time.Second)
		defer tc.Stop()
	loop:
		for {
			if n.ReadyNode == PARENTCOUNTBASE {
				break loop
			}
			select {
			case node := <-n.Recv:
				value := n.ParentsCacheBase[node.NodeIndex]
				n.ReadyNode += value.Counter
				n.ParentsCacheBase[node.NodeIndex].Data = node.Hash
				delete(tmpParentCacheBase, node.NodeIndex)
				log.Debugf("Recv layer %v node %v this layer %v node %v base cache %v tmpParentCacheBase %v exp cache %v readynode %v\n", node.Layer, node.NodeIndex, n.Layer, n.NodeIndex, n.ParentsCacheBase, tmpParentCacheBase, n.ParentsCacheEXP, n.ReadyNode)
			case <-tc.C:
				log.Debugf("loop default layer %v node %v tmpParentCacheBase %v", n.Layer, n.NodeIndex, tmpParentCacheBase)
			}
			for k, _ := range tmpParentCacheBase {
				var ll uint32 = 1
				if n.Layer%2 == 0 {
					ll = 2
				}
				dbkey := fmt.Sprintf("%d-%d", ll, k)
				data, err := n.Db.Get([]byte(dbkey), k, nil, false)
				if err != nil {
					log.Debug(fmt.Errorf("get layer %v node %v base parent node %v error %v", n.Layer, n.NodeIndex, k, err))
					continue loop
				}
				var td [NODESIZE]byte
				copy(td[:], data[:NODESIZE])
				n.ParentsCacheBase[k].Data = td
				n.ReadyNode += n.ParentsCacheBase[k].Counter
				delete(tmpParentCacheBase, k)
			}
		}
		if n.Layer == 1 {
			var parents [PARENTCOUNT][NODESIZE]byte
			for i := 0; i < PARENTCOUNTBASE; i++ {
				log.Debug(n.ParentsIndex[i])
				parents[i] = n.ParentsCacheBase[n.ParentsIndex[i]].Data
			}
			for i := 0; i < PARENTCOUNTBASE; i++ {
				state = lib.GoShaLayer(state, PARENTCOUNTBASE, parents)
			}
			nodehash = lib.GoFinishWith(state, uint64(HEADCOUNT<<8+PARENTCOUNTBASE<<8*6), parents[0])
		} else {
			var parents [PARENTCOUNT][NODESIZE]byte
			for i := 0; i < PARENTCOUNTBASE; i++ {
				parents[i] = n.ParentsCacheBase[n.ParentsIndex[i]].Data
			}
			for !n.ExpOk {
				log.Debugf("layer %v node %v exp_parent not ready\n", n.Layer, n.NodeIndex)
				time.Sleep(3 * time.Second)
			}
			for i := PARENTCOUNTBASE; i < PARENTCOUNT; i++ {
				parents[i] = n.ParentsCacheEXP[n.ParentsIndex[i]].Data
			}
			state = lib.GoShaLayer(state, PARENTCOUNT, parents)
			state = lib.GoShaLayer(state, PARENTCOUNT, parents)
			state = lib.GoShaLayer(state, PARENTCOUNTEXP, parents)
			nodehash = lib.GoFinishWith(state, uint64(HEADCOUNT<<8+PARENTCOUNT<<8*2+PARENTCOUNTEXP<<8), parents[8])
		}
	}
	log.Debugf("\n-------------layer %v node %v nodehash %v-------------\n", n.Layer, n.NodeIndex, nodehash)
	var ll uint32 = 1
	if n.Layer%2 == 0 {
		ll = 2
	}
	dbkey := fmt.Sprintf("%d-%d", ll, n.NodeIndex)
	err := n.Db.Put([]byte(dbkey), nodehash[:], n.NodeIndex, nil)
	if err != nil {
		log.Debug(fmt.Errorf("layer %v node %v db put error %v", n.Layer, n.NodeIndex, err))
	}
	n.Pub <- Node{Layer: n.Layer, NodeIndex: n.NodeIndex, Hash: nodehash}
	go func() {
		defer func() {
			recover()
		}()
		n.ScheChan <- nil
	}()
	n.WriteChan <- nil
	if n.LastNode {
		n.WriteChan <- "end"
	}
	log.Debug("put schechan")
}

func (n *Node) FillExp() {
	if n.Layer == 1 {
		return
	}
	go func() {
		for i := PARENTCOUNTBASE; i < PARENTCOUNT; i++ {
			nid := n.ParentsIndex[i]
			var ll uint32 = 2
			if n.Layer%2 == 0 {
				ll = 1
			}
			dbkey := fmt.Sprintf("%d-%d", ll, nid)
			ln, err := n.Db.Get([]byte(dbkey), nid, nil, true)
			for err != nil {
				log.Debug(fmt.Errorf("get layer %v node %v parent %v error %v", n.Layer, n.NodeIndex, i, err))
				time.Sleep(3 * time.Second)
				ln, err = n.Db.Get([]byte(dbkey), nid, nil, true)
			}
			var tb [NODESIZE]byte
			copy(tb[:], ln[:])
			n.ParentsCacheEXP[nid].Data = tb
		}
		n.ExpOk = true
	}()
}

type ParentsMapCache struct {
	LayerSize       int
	NodeCnt         uint64
	ParentCachePath string
	DbPath          string
	ParallelCnt     uint64
}

func (pc *ParentsMapCache) Start(providerID, sectorID, ticket, commd []byte) {
	at, err := mmap.Open(pc.ParentCachePath)
	if err != nil {
		log.Debug(err)
		return
	}

	db, err := leveldb.OpenFile(pc.DbPath, nil)
	if err != nil {
		log.Debug(fmt.Errorf("open leveldb error %v", err))
		return
	}
	defer db.Close()

	var nodeCnt uint64 = pc.NodeCnt

	cachedb.CacheSize = nodeCnt / 32
	cachedb.Cache = make(map[uint64][]byte)
	cachedb.LastLayerCache = make([]map[string][]byte, cachedb.CacheSize)
	cachedb.Db = db

	layerSize := pc.LayerSize
	var wait sync.WaitGroup
	var parallelCnt uint64 = pc.ParallelCnt
	if parallelCnt > nodeCnt {
		parallelCnt = nodeCnt
	}
	// ff, err := os.Open("2-layer-1")
	// if err != nil {
	// 	log.Debug(fmt.Errorf("open 512-layer-1 error %v", err))
	// 	return
	// }
	// defer ff.Close()
	for i := 1; i < layerSize; i++ {
		var pub chan Node
		var assiascnt uint64 = 0
		scheChan := make(chan interface{})
		wf := func(layer int, db *CacheDB) chan interface{} {
			f, err := os.OpenFile(fmt.Sprintf("layer-%d", layer), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
			if err != nil {
				log.Debug(fmt.Errorf("open file layer %d error %v", i, err))
				return nil
			}
			writeChan := make(chan interface{})
			wait.Add(1)
			go func() {
				defer wait.Done()
				bt := time.Now()
				log.Infof("layer %v begin %v", layer, bt.Format(timeTemplate))
				var node uint64 = 0
				leadEnd := false
				for {
					if node == pc.NodeCnt {
						break
					}
					if !leadEnd {
						r := <-writeChan
						if r != nil {
							leadEnd = true
						}
					}
					var ll uint32 = 1
					if layer%2 == 0 {
						ll = 2
					}
					data, err := db.Get([]byte(fmt.Sprintf("%d-%d", ll, node)), node, nil, false)
					for err != nil {
						log.Debugf("db get error %v", err)
						time.Sleep(time.Second)
						data, err = db.Get([]byte(fmt.Sprintf("%d-%d", ll, node)), node, nil, false)
					}
					log.Debugf("db.Get layer %d node %d data %v\n", layer, node, data)
					// var vanillabuf [NODESIZE]byte
					// n, _ := ff.ReadAt(vanillabuf[:], int64(node*NODESIZE))
					// if hex.EncodeToString(data[:]) != hex.EncodeToString(vanillabuf[:]) {
					// 	log.Infof("data %v vanilla %v read %v node %v", data, vanillabuf, n, node)
					// 	os.Exit(0)
					// }
					_, err = f.Write(data[:])
					if err != nil {
						log.Debug(fmt.Errorf("layer %d write to file error %v", i, err))
					}
					// buf = append(buf, data[:]...)
					node++
				}
				// _, err := f.Write(buf)
				// if err != nil {
				// 	log.Debug(fmt.Errorf("layer %d write to file error %v", i, err))
				// }
				et := time.Now()
				log.Infof("commplit %v time %v", f.Name(), et.Sub(bt))
				f.Close()
				close(writeChan)
			}()
			return writeChan
		}
		writeChan := wf(i, cachedb)
		var async = func(buf []byte, layer int, node uint64, recv chan Node) chan Node {
			n := Node{}
			n.Layer = uint32(layer)
			n.NodeIndex = uint64(node)
			n.ParentsCacheBase = make(map[uint64]*NodeIndex)
			n.ParentsCacheEXP = make(map[uint64]*NodeIndex)
			n.ParentsIndex = make([]uint64, PARENTCOUNT)
			n.Recv = recv
			n.Pub = make(chan Node, PARENTCOUNT)
			n.Pmc = pc
			n.Db = cachedb
			n.LastNode = node == nodeCnt-1
			n.ScheChan = scheChan
			n.WriteChan = writeChan
			if node > 0 {
				for i := 0; i < PARENTCOUNT; i++ {
					start := i * 4
					end := start + 4
					node := binary.LittleEndian.Uint32(buf[start:end])
					if i < PARENTCOUNTBASE {
						_, ok := n.ParentsCacheBase[uint64(node)]
						if !ok {
							n.ParentsCacheBase[uint64(node)] = &NodeIndex{Counter: 1}
						} else {
							n.ParentsCacheBase[uint64(node)].Counter += 1
						}
					} else {
						_, ok := n.ParentsCacheEXP[uint64(node)]
						if !ok {
							n.ParentsCacheEXP[uint64(node)] = &NodeIndex{Counter: 1}
						} else {
							n.ParentsCacheEXP[uint64(node)].Counter++
						}
					}
					n.ParentsIndex[i] = uint64(node)
				}
				n.FillExp()
			}
			go func() {
				n.Start(providerID, sectorID, ticket, commd)
			}()
			return n.Pub
		}
		var j uint64 = 0
		for ; j < parallelCnt; j++ {
			buf := make([]byte, PARENTCOUNT*4)
			at.ReadAt(buf, int64(assiascnt*PARENTCOUNT*4))
			pub = async(buf, i, assiascnt, pub)
			assiascnt++
		}
		for assiascnt < nodeCnt {
			<-scheChan
			buf := make([]byte, PARENTCOUNT*4)
			at.ReadAt(buf, int64(assiascnt*PARENTCOUNT*4))
			pub = async(buf, i, assiascnt, pub)
			assiascnt++
		}
		close(scheChan)
		wait.Wait()
	}
}

func Test2() {

	f, err := os.OpenFile("cpuprofile", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		log.Info(err)
		return
	}
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	// commd := []byte{252, 126, 146, 130, 150, 229, 22, 250, 173, 233, 134, 178, 143, 146, 212, 74, 79, 36, 185, 53, 72, 82, 35, 55, 106, 121, 144, 39, 188, 24, 248, 51}                                                     //2kib
	// pc := ParentsMapCache{ParentCachePath: "/var/tmp1/filecoin-parents/v28-sdr-parent-652bae61e906c0732e9eb95b1217cfa6afcce221ff92a8aedf62fa778fa765bc.cache", NodeCnt: 64, LayerSize: 3, DbPath: "./test", ParallelCnt: 5} // 2KiB

	// commd := []byte{57, 86, 14, 123, 19, 169, 59, 7, 162, 67, 253, 39, 32, 255, 167, 203, 62, 29, 46, 80, 90, 179, 98, 158, 121, 244, 99, 19, 81, 44, 218, 6}                                                                         //512mib
	// pc := ParentsMapCache{ParentCachePath: "/var/tmp1/filecoin-parents/v28-sdr-parent-016f31daba5a32c5933a4de666db8672051902808b79d51e9b97da39ac9981d3.cache", NodeCnt: 16777216, LayerSize: 3, DbPath: "./test", ParallelCnt: 10000} // 512MiB 16777216

	commd := []byte{1, 129, 226, 3, 146, 32, 32, 7, 126, 95, 222, 53, 197, 10, 147, 3, 165, 80, 9, 227, 73, 138, 78, 190, 223, 243, 156, 66, 183, 16, 183, 48, 216, 236, 122, 199, 175, 166, 62}                                       //32gib
	pc := ParentsMapCache{ParentCachePath: "/var/tmp1/filecoin-parents/v28-sdr-parent-55c7d1e6bb501cc8be94438f89b577fddda4fafa71ee9ca72eabe2f0265aefa6.cache", NodeCnt: 1073741824, LayerSize: 12, DbPath: "./test", ParallelCnt: 100} // 32GiB 1073741824

	providerID := []byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	ticket := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	sectorID := []byte{0, 0, 0, 0, 0, 0, 0, 0}
	pc.Start(providerID, sectorID, ticket, commd)

	fm, err := os.OpenFile("memprofile", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal(err)
	}
	pprof.WriteHeapProfile(fm)
	fm.Close()
}

func Test() {
	providerID := []byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	commd := []byte{252, 126, 146, 130, 150, 229, 22, 250, 173, 233, 134, 178, 143, 146, 212, 74, 79, 36, 185, 53, 72, 82, 35, 55, 106, 121, 144, 39, 188, 24, 248, 51}
	ticket := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	sectorID := []byte{0, 0, 0, 0, 0, 0, 0, 0}
	n := Node{}
	n.ParentsIndex = make([]uint64, 0, PARENTCOUNT)
	n.ParentsCacheBase = make(map[uint64]*NodeIndex)
	n.ParentsCacheBase[0] = &NodeIndex{Counter: 6}
	n.Layer = 1
	n.NodeIndex = 1
	n.Recv = make(chan Node, 14)
	n0 := [32]byte{249, 30, 216, 215, 172, 15, 129, 92, 47, 206, 75, 148, 47, 214, 131, 207, 23, 54, 158, 165, 117, 33, 174, 199, 191, 211, 60, 250, 113, 15, 216, 14}
	n.Recv <- Node{Layer: 1, NodeIndex: 0, Hash: n0}
	n.Start(providerID, sectorID, ticket, commd)
}
