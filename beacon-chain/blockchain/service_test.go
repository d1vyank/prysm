package blockchain

import (
	"context"
	"io/ioutil"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/prysmaticlabs/prysm/beacon-chain/params"
	"github.com/prysmaticlabs/prysm/beacon-chain/powchain"
	"github.com/prysmaticlabs/prysm/beacon-chain/types"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/database"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	"github.com/sirupsen/logrus"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetOutput(ioutil.Discard)
}

type mockClient struct{}

func (f *mockClient) SubscribeNewHead(ctx context.Context, ch chan<- *gethTypes.Header) (ethereum.Subscription, error) {
	return new(event.Feed).Subscribe(ch), nil
}

func (f *mockClient) BlockByHash(ctx context.Context, hash common.Hash) (*gethTypes.Block, error) {
	head := &gethTypes.Header{Number: big.NewInt(0), Difficulty: big.NewInt(100)}
	return gethTypes.NewBlockWithHeader(head), nil
}

func (f *mockClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- gethTypes.Log) (ethereum.Subscription, error) {
	return new(event.Feed).Subscribe(ch), nil
}

func (f *mockClient) LatestBlockHash() common.Hash {
	return common.BytesToHash([]byte{'A'})
}

func init() {
	logrus.SetLevel(logrus.DebugLevel)
}

func TestStartStop(t *testing.T) {
	ctx := context.Background()

	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
	}
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	chainService, err := NewChainService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}
	chainService.Start()

	cfg = &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}
	chainService, err = NewChainService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}
	chainService.Start()

	if len(chainService.CurrentActiveState().RecentBlockHashes()) != 128 {
		t.Errorf("incorrect recent block hashes")
	}

	if len(chainService.CurrentCrystallizedState().Validators()) != params.BootstrappedValidatorsCount {
		t.Errorf("incorrect default validator size")
	}
	blockExists, err := chainService.ContainsBlock([32]byte{})
	if err != nil {
		t.Fatalf("unable to check if block exists: %v", err)
	}
	if blockExists {
		t.Errorf("chain is not empty")
	}
	hasState, err := chainService.HasStoredState()
	if err != nil {
		t.Fatalf("calling HasStoredState failed")
	}
	if hasState {
		t.Errorf("has stored state should return false")
	}
	chainService.CanonicalBlockFeed()
	chainService.CanonicalCrystallizedStateFeed()

	chainService, _ = NewChainService(ctx, cfg)

	active := types.NewActiveState(&pb.ActiveState{RecentBlockHashes: [][]byte{{'A'}}}, make(map[[32]byte]*types.VoteCache))

	activeStateHash, err := active.Hash()
	if err != nil {
		t.Fatalf("Cannot hash active state: %v", err)
	}
	chainService.chain.SetActiveState(active)

	crystallized := types.NewCrystallizedState(&pb.CrystallizedState{LastStateRecalc: 10000})
	crystallizedStateHash, err := crystallized.Hash()
	if err != nil {
		t.Fatalf("Cannot hash crystallized state: %v", err)
	}
	chainService.chain.SetCrystallizedState(crystallized)

	parentBlock := types.NewBlock(nil)
	parentHash, _ := parentBlock.Hash()

	block := types.NewBlock(&pb.BeaconBlock{
		SlotNumber:            2,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
	})
	if err := chainService.SaveBlock(block); err != nil {
		t.Errorf("save block should have failed")
	}

	// Save states so HasStoredState state should return true.
	chainService.chain.SetActiveState(types.NewActiveState(&pb.ActiveState{}, make(map[[32]byte]*types.VoteCache)))
	chainService.chain.SetCrystallizedState(types.NewCrystallizedState(&pb.CrystallizedState{}))
	hasState, _ = chainService.HasStoredState()
	if !hasState {
		t.Errorf("has stored state should return false")
	}

	if err := chainService.Stop(); err != nil {
		t.Fatalf("unable to stop chain service: %v", err)
	}

	// The context should have been canceled.
	if chainService.ctx.Err() == nil {
		t.Error("context was not canceled")
	}
}

func TestFaultyStop(t *testing.T) {
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, err := NewChainService(ctx, cfg)
	if err != nil {
		t.Fatalf("unable to setup chain service: %v", err)
	}

	chainService.Start()

	chainService.chain.SetActiveState(types.NewActiveState(nil, make(map[[32]byte]*types.VoteCache)))

	err = chainService.Stop()
	if err == nil {
		t.Errorf("chain stop should have failed with persist active state")
	}

	chainService.chain.SetActiveState(types.NewActiveState(&pb.ActiveState{}, make(map[[32]byte]*types.VoteCache)))

	chainService.chain.SetCrystallizedState(types.NewCrystallizedState(nil))
	err = chainService.Stop()
	if err == nil {
		t.Errorf("chain stop should have failed with persist crystallized state")
	}
}

func TestRunningChainService(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	active := types.NewGenesisActiveState()
	crystallized, err := types.NewGenesisCrystallizedState()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}
	chainService, _ := NewChainService(ctx, cfg)

	genesis, err := beaconChain.GenesisBlock()
	if err != nil {
		t.Fatalf("unable to get canonical head: %v", err)
	}

	parentHash, err := genesis.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of canonical head: %v", err)
	}

	block := types.NewBlock(&pb.BeaconBlock{
		SlotNumber:            1,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
		Attestations: []*pb.AggregatedAttestation{{
			Slot:             0,
			AttesterBitfield: []byte{128, 0},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()
	if err := chainService.SaveBlock(block); err != nil {
		t.Fatal(err)
	}

	chainService.incomingBlockChan <- block
	chainService.cancel()
	exitRoutine <- true

	hash, err := block.Hash()
	if err != nil {
		t.Fatal("Can not hash the block")
	}
	slot, err := chainService.GetBlockSlotNumber(hash)
	if err != nil {
		t.Fatal("Can not get block slot number")
	}
	if slot != block.SlotNumber() {
		t.Errorf("Block slot number mismatched, wanted 1, got slot %d", block.SlotNumber())
	}
	if _, err := chainService.GetBlockSlotNumber([32]byte{}); err == nil {
		t.Fatal("Get block slot number should have failed with nil hash")
	}

	testutil.AssertLogsContain(t, hook, "Finished processing state for candidate block")
}

func TestUpdateHead(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)

	}
	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}
	cfg := &Config{
		BeaconBlockBuf:   0,
		IncomingBlockBuf: 0,
		BeaconDB:         db.DB(),
		Chain:            beaconChain,
		Web3Service:      web3Service,
	}
	chainService, _ := NewChainService(ctx, cfg)

	active := types.NewGenesisActiveState()
	crystallized, err := types.NewGenesisCrystallizedState()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}
	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	parentHash := [32]byte{'a'}

	block := types.NewBlock(&pb.BeaconBlock{
		SlotNumber:            64,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		ParentHash:            parentHash[:],
		PowChainRef:           []byte("a"),
	})

	chainService.candidateBlock = block
	chainService.candidateActiveState = active
	chainService.candidateCrystallizedState = crystallized

	chainService.updateHead()
	testutil.AssertLogsContain(t, hook, "Canonical block determined")

	if chainService.candidateBlock != nilBlock {
		t.Error("Candidate Block unable to be reset")
	}
}

func TestProcessingBlocks(t *testing.T) {
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	active := types.NewGenesisActiveState()
	crystallized, err := types.NewGenesisCrystallizedState()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	block0 := types.NewBlock(&pb.BeaconBlock{
		SlotNumber: 3,
	})
	if saveErr := beaconChain.saveBlock(block0); saveErr != nil {
		t.Fatalf("Cannot save block: %v", saveErr)
	}
	block0Hash, err := block0.Hash()
	if err != nil {
		t.Fatalf("Failed to compute block's hash: %v", err)
	}

	block1 := types.NewBlock(&pb.BeaconBlock{
		ParentHash:            block0Hash[:],
		SlotNumber:            4,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AggregatedAttestation{{
			Slot:             0,
			AttesterBitfield: []byte{16, 0},
			ShardId:          0,
		}},
	})
	block1Hash, err := block1.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of block 1: %v", err)
	}

	// Add 1 more attestation field for slot2
	block2 := types.NewBlock(&pb.BeaconBlock{
		ParentHash: block1Hash[:],
		SlotNumber: 5,
		Attestations: []*pb.AggregatedAttestation{
			{Slot: 0, AttesterBitfield: []byte{8, 0}, ShardId: 0},
			{Slot: 1, AttesterBitfield: []byte{8, 0}, ShardId: 0},
		}})
	block2Hash, err := block2.Hash()
	if err != nil {
		t.Fatalf("unable to get hash of block 1: %v", err)
	}

	// Add 1 more attestation field for slot3
	block3 := types.NewBlock(&pb.BeaconBlock{
		ParentHash: block2Hash[:],
		SlotNumber: 6,
		Attestations: []*pb.AggregatedAttestation{
			{Slot: 0, AttesterBitfield: []byte{4, 0}, ShardId: 0},
			{Slot: 1, AttesterBitfield: []byte{4, 0}, ShardId: 0},
			{Slot: 2, AttesterBitfield: []byte{4, 0}, ShardId: 0},
		}})

	chainService.incomingBlockChan <- block1
	chainService.incomingBlockChan <- block2
	chainService.incomingBlockChan <- block3

	chainService.cancel()
	exitRoutine <- true

	// We should have 3 pending attestations from blocks 1 and 2
	if len(beaconChain.ActiveState().PendingAttestations()) != 3 {
		t.Fatalf("Active state should have 3 pending attestation: %d", len(beaconChain.ActiveState().PendingAttestations()))
	}

	// Check that there are 6 pending attestations for the candidate state
	if len(chainService.candidateActiveState.PendingAttestations()) != 6 {
		t.Fatalf("Candidate active state should have 6 pending attestations: %d", len(chainService.candidateActiveState.PendingAttestations()))
	}
}

func TestProcessAttestationBadBlock(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	active := types.NewGenesisActiveState()
	crystallized, err := types.NewGenesisCrystallizedState()
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	block0 := types.NewBlock(&pb.BeaconBlock{
		SlotNumber: 5,
	})
	if saveErr := beaconChain.saveBlock(block0); saveErr != nil {
		t.Fatalf("Cannot save block: %v", saveErr)
	}
	parentHash, err := block0.Hash()
	if err != nil {
		t.Fatalf("Failed to compute block's hash: %v", err)
	}

	block1 := types.NewBlock(&pb.BeaconBlock{
		ParentHash:            parentHash[:],
		SlotNumber:            1,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AggregatedAttestation{{
			Slot:             10,
			AttesterBitfield: []byte{},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	chainService.incomingBlockChan <- block1

	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "attestation slot number can't be higher than parent block's slot number. Found: 10, Needed lower than: 5")
}

func TestEnterCycleTransition(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	genesisBlock, _ := beaconChain.GenesisBlock()
	active := beaconChain.ActiveState()
	crystallized := beaconChain.CrystallizedState()

	parentHash, _ := genesisBlock.Hash()
	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()

	block1 := types.NewBlock(&pb.BeaconBlock{
		ParentHash:            parentHash[:],
		SlotNumber:            64,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AggregatedAttestation{{
			Slot:             0,
			AttesterBitfield: []byte{128, 0},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	chainService.incomingBlockChan <- block1

	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "Entering cycle transition")
}

func TestEnterDynastyTransition(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}
	var shardCommitteeForSlots []*pb.ShardAndCommitteeArray
	for i := 0; i < 300; i++ {
		shardCommittee := &pb.ShardAndCommitteeArray{
			ArrayShardAndCommittee: []*pb.ShardAndCommittee{
				{ShardId: 0, Committee: []uint32{0, 1, 2, 3}},
				{ShardId: 1, Committee: []uint32{0, 1, 2, 3}},
				{ShardId: 2, Committee: []uint32{0, 1, 2, 3}},
				{ShardId: 3, Committee: []uint32{0, 1, 2, 3}},
			},
		}
		shardCommitteeForSlots = append(shardCommitteeForSlots, shardCommittee)
	}

	var validators []*pb.ValidatorRecord
	for i := 0; i < 5; i++ {
		validators = append(validators, &pb.ValidatorRecord{StartDynasty: 0, EndDynasty: params.DefaultEndDynasty})
	}

	chainService, _ := NewChainService(ctx, cfg)
	crystallized := types.NewCrystallizedState(
		&pb.CrystallizedState{
			DynastyStart:               1,
			LastFinalizedSlot:          2,
			ShardAndCommitteesForSlots: shardCommitteeForSlots,
			Validators:                 validators,
			LastStateRecalc:            150,
			CrosslinkRecords: []*pb.CrosslinkRecord{
				{Slot: 2},
				{Slot: 2},
				{Slot: 2},
				{Slot: 2},
			},
		},
	)

	var recentBlockhashes [][]byte
	for i := 0; i < 257; i++ {
		recentBlockhashes = append(recentBlockhashes, []byte{'A'})
	}
	active := types.NewActiveState(
		&pb.ActiveState{
			RecentBlockHashes: recentBlockhashes,
			PendingAttestations: []*pb.AggregatedAttestation{
				{Slot: 100, AttesterBitfield: []byte{0}},
				{Slot: 101, AttesterBitfield: []byte{0}},
				{Slot: 102, AttesterBitfield: []byte{0}},
				{Slot: 103, AttesterBitfield: []byte{0}},
			},
		}, nil,
	)

	block0 := types.NewBlock(&pb.BeaconBlock{
		SlotNumber: 202,
	})
	if saveErr := beaconChain.saveBlock(block0); saveErr != nil {
		t.Fatalf("Cannot save block: %v", saveErr)
	}
	block0Hash, err := block0.Hash()
	if err != nil {
		t.Fatalf("Failed to compute block's hash: %v", err)
	}

	activeStateHash, _ := active.Hash()
	crystallizedStateHash, _ := crystallized.Hash()
	if err := chainService.chain.SetCrystallizedState(crystallized); err != nil {
		t.Fatalf("unable to save crystallized state %v", err)
	}
	if err := chainService.chain.SetActiveState(active); err != nil {
		t.Fatalf("unable to save active state %v", err)
	}

	block1 := types.NewBlock(&pb.BeaconBlock{
		ParentHash:            block0Hash[:],
		SlotNumber:            params.MinDynastyLength + 1,
		ActiveStateHash:       activeStateHash[:],
		CrystallizedStateHash: crystallizedStateHash[:],
		Attestations: []*pb.AggregatedAttestation{{
			Slot:             200,
			AttesterBitfield: []byte{32},
			ShardId:          0,
		}},
	})

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	chainService.incomingBlockChan <- block1

	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "Entering dynasty transition")
}

func TestIncomingAttestation(t *testing.T) {
	hook := logTest.NewGlobal()
	ctx := context.Background()
	config := &database.DBConfig{DataDir: "", Name: "", InMemory: true}
	db, err := database.NewDB(config)
	if err != nil {
		t.Fatalf("could not setup beaconDB: %v", err)
	}

	endpoint := "ws://127.0.0.1"
	client := &mockClient{}
	web3Service, err := powchain.NewWeb3Service(ctx, &powchain.Web3ServiceConfig{Endpoint: endpoint, Pubkey: "", VrcAddr: common.Address{}}, client, client, client)
	if err != nil {
		t.Fatalf("unable to set up web3 service: %v", err)
	}
	beaconChain, err := NewBeaconChain(db.DB())
	if err != nil {
		t.Fatalf("could not register blockchain service: %v", err)
	}

	cfg := &Config{
		BeaconBlockBuf: 0,
		BeaconDB:       db.DB(),
		Chain:          beaconChain,
		Web3Service:    web3Service,
	}

	chainService, _ := NewChainService(ctx, cfg)

	exitRoutine := make(chan bool)
	go func() {
		chainService.blockProcessing(chainService.ctx.Done())
		<-exitRoutine
	}()

	attestation := types.NewAttestation(
		&pb.AggregatedAttestation{
			Slot:           1,
			ShardId:        1,
			ShardBlockHash: []byte{'A'},
		})

	chainService.incomingAttestationChan <- attestation
	chainService.cancel()
	exitRoutine <- true

	testutil.AssertLogsContain(t, hook, "Relaying attestation")
}
