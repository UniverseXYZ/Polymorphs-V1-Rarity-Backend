package services

import (
	"context"
	"encoding/json"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"math/big"
	"net/http"
	"os"
	"polymorphs-rarity-v1/constants"
	"polymorphs-rarity-v1/dlt"
	"polymorphs-rarity-v1/handlers"
	"polymorphs-rarity-v1/helpers"
	"polymorphs-rarity-v1/metadata"
	"polymorphs-rarity-v1/models"
	"polymorphs-rarity-v1/store"
	"polymorphs-rarity-v1/structs"
	"strings"
	"sync"
)

// RecoverProcess is the main function which handles the polling and processing of mint and morph events
func RecoverProcess(ethClient *dlt.EthereumClient, contractAbi abi.ABI, instance *store.Store, address string, configService *structs.ConfigService,
	dbInfo structs.DBInfo, txState map[string]map[uint]bool, morphCostMap map[string]float32) error {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	var wg sync.WaitGroup
	mintsMutex := structs.MintsMutex{TokensMap: make(map[string]bool)}
	eventLogsMutex := structs.EventLogsMutex{EventLogs: []types.Log{}}
	genesMap := make(map[string]string)
	tokenToMorphEvent := make(map[string]types.Log)

	lastProcessedBlockNumber, err := collectEvents(ethClient, address, dbInfo.PolymorphDBName, &wg, &eventLogsMutex)
	if err != nil {
		log.WithFields(log.Fields{"original error: ": err}).Error("Error collecting events from ethereum network")
		return err
	}

	log.WithFields(log.Fields{
		"network": "Ethereum",
	}).Infof("Last processed block number: [%v]", lastProcessedBlockNumber)

	mintsLogs := make([]types.Log, 0)

	// Persist mints
	for _, ethLog := range eventLogsMutex.EventLogs {
		eventSig := ethLog.Topics[0].String()
		switch eventSig {
		case constants.MintEvent.Signature:
			wg.Add(1)
			processMint(ethLog, &wg, contractAbi, configService, &mintsMutex)
			mintsLogs = append(mintsLogs, ethLog)
		}
	}

	wg.Wait()
	if len(mintsMutex.Documents) > 0 {
		errMintEvents := handlers.PersistMintEvents(mintsMutex.Documents, dbInfo.PolymorphDBName, dbInfo.RarityCollectionName)
		if errMintEvents != nil {
			return errMintEvents
		}
	}

	if len(mintsMutex.Transactions) > 0 {
		err = handlers.SaveTransactions(mintsMutex.Transactions, dbInfo.PolymorphDBName, dbInfo.TransactionsCollectionName)
		if err != nil {
			return err
		}
	}

	// Sort polymorphs
	helpers.SortMorphEvents(eventLogsMutex.EventLogs)
	// Persist Morphs
	for _, ethLog := range eventLogsMutex.EventLogs {
		eventSig := ethLog.Topics[0].String()
		switch eventSig {
		case constants.MorphEvent.Signature:
			err = processInitialMorphs(ethLog, ethClient, contractAbi, instance, configService, dbInfo, txState, genesMap, tokenToMorphEvent, morphCostMap)
			if err != nil {
				return err
			}
		}
	}

	// Persist final scrambles
	for id := range genesMap {
		ethLog := tokenToMorphEvent[id]
		err = processFinalMorphs(ethLog, ethClient, contractAbi, instance, configService, dbInfo, genesMap, morphCostMap)
		if err != nil {
			log.Error("Error processing final morphs. ", err)
			return err
		}
	}

	// Persist Ranking
	handlers.UpdateAllRanking(dbInfo.PolymorphDBName, dbInfo.RarityCollectionName)
	// Persist block
	lastPersistedBlockEth, err := handlers.CreateOrUpdateLastProcessedBlock(lastProcessedBlockNumber, dbInfo.PolymorphDBName, dbInfo.BlocksCollectionName)
	if err != nil {
		log.WithFields(log.Fields{"network:": "Ethereum", "original error: ": err}).Error("error creating/updating last processed block...")
		return err
	}
	log.WithFields(log.Fields{
		"network": "Ethereum",
	}).Infof("Successfully persisted new last processed block number [%v]: ", lastPersistedBlockEth)

	return nil

}

// processMint is the core function for processing mint events metadata. It unpacks event data, calculates rarity score, prepares database entity but doesn't persist it
//
// Uses Mutes and WaitGroup in order to process events faster and prevent race conditions.
func processMint(mintEvent types.Log, wg *sync.WaitGroup, contractAbi abi.ABI, configService *structs.ConfigService, mintsMutex *structs.MintsMutex) {
	defer wg.Done()
	var event structs.PolymorphEvent
	mintsMutex.Mutex.Lock()
	contractAbi.UnpackIntoInterface(&event, constants.MintEvent.Name, mintEvent.Data)
	event.MorphId = mintEvent.Topics[1].Big()
	event.OldGene = big.NewInt(0)
	if event.NewGene.String() != "0" && !mintsMutex.TokensMap[event.MorphId.String()] {
		g := metadata.Genome(event.NewGene.String())
		metadataJson := (&g).Metadata(event.MorphId.String(), configService)
		rarityResult := CalulateRarityScore(metadataJson.Attributes, true)
		mintEntity := helpers.CreateMorphEntity(event, metadataJson, true, rarityResult)

		mintsMutex.Mints = append(mintsMutex.Mints, mintEntity)
		mintsMutex.TokensMap[event.MorphId.String()] = true
		var bdoc interface{}
		jsonEntity, _ := json.Marshal(mintEntity)
		bson.UnmarshalExtJSON(jsonEntity, false, &bdoc)
		mintsMutex.Documents = append(mintsMutex.Documents, bdoc)

		transaction := models.Transaction{
			BlockNumber: mintEvent.BlockNumber,
			TxIndex:     mintEvent.TxIndex,
			TxHash:      mintEvent.TxHash.Hex(),
			LogIndex:    mintEvent.Index,
		}
		var txBdoc interface{}
		jsonTx, _ := json.Marshal(transaction)
		bson.UnmarshalExtJSON(jsonTx, false, &txBdoc)

		mintsMutex.Transactions = append(mintsMutex.Transactions, txBdoc)

	} else {
		log.Warn("Empty gene mint event for morph id: " + event.MorphId.String())
	}
	mintsMutex.Mutex.Unlock()
}

// processInitialMorphs is the core function for processing morph events. It's contains the trickiest logic in the app because TokenMorphed event emits the old gene in both the new gene and old gene parameters.
//
// We're interested in morph events with event type 1. (0 is Morph, 2 is Transfer)
//
// We can't be sure how many morph events each polymorph has. This is why we have to process the morph event only after we've got a chronological pair of genes.
// In oldGenesMap we keep track of the tokenId -> gene mappings.
//
// If there isn't already existing mapping for the token - we save the current gene in the mapping and proceed to saving the information for the newest gene received from the contract.
//
// If there is existing mapping - this means we've got a chronological pair of morph events of a polymorph and we can process them to find out which traits have changed.
// We compare the old and the new gene and create a history snapshot of the changes, persists the increment scramble/morph in the rarity collection and persists the event transaction in the transactions collection
//
// We save the new gene to the oldGenesMap and repeat the process for the next event for this polymorph.
//
// !! It's important to note which gene is passed as the new one and which as the old one in order to understand how the logic works.
func processInitialMorphs(morphEvent types.Log, ethClient *dlt.EthereumClient, contractAbi abi.ABI, instance *store.Store, configService *structs.ConfigService, dbInfo structs.DBInfo,
	txState map[string]map[uint]bool, oldGenesMap map[string]string, tokenToMorphEvent map[string]types.Log, morphCostMap map[string]float32) error {
	var mEvent structs.MorphedEvent
	err := contractAbi.UnpackIntoInterface(&mEvent, constants.MorphEvent.Name, morphEvent.Data)
	if err != nil {
		log.Println("Error unpacking into interface when processing initial morphs. ", err)
		return err
	}

	// 1 is Morph event
	txMap, hasTxMap := txState[morphEvent.TxHash.Hex()]
	if mEvent.EventType == 1 && (!hasTxMap || !txMap[morphEvent.Index]) {
		log.Println()
		log.Printf("\nBlock Num: %v\nTxIndex: %v\nEventIndex:%v\n", morphEvent.BlockNumber, morphEvent.TxIndex, morphEvent.Index)

		mId := morphEvent.Topics[1].Big()

		// This will get the newest gene
		result, err := instance.GeneOf(&bind.CallOpts{}, mId)
		if err != nil {
			log.Println(err)
		}
		mEvent.NewGene = result
		var geneDifferences, geneIdx int
		newAttr, oldAttr := structs.Attribute{}, structs.Attribute{}
		if oldGenesMap[mId.String()] != "" {
			geneIdx, geneDifferences = helpers.DetectGeneDifferences(oldGenesMap[mId.String()], mEvent.OldGene.String())
			if geneDifferences <= 2 {
				newAttr, oldAttr = helpers.GetAttribute(mEvent.OldGene.String(), oldGenesMap[mId.String()], geneIdx, configService)
			}
			block, err := ethClient.Client.BlockByNumber(context.Background(), big.NewInt(int64(morphEvent.BlockNumber)))
			if err != nil {
				log.Error("Error fetching latest block number. ", err)
				return err
			}
			polySnapshot := helpers.CreateMorphSnapshot(geneDifferences, mId.String(), mEvent.OldGene.String(), oldGenesMap[mId.String()], block.Time(), oldAttr, newAttr, morphCostMap, configService)
			handlers.SavePolymorphHistory(polySnapshot, dbInfo.PolymorphDBName, dbInfo.HistoryCollectionName)
			handlers.SaveMorphPrice(models.MorphCost{TokenId: mId.String(), Price: morphCostMap[mId.String()]}, dbInfo.PolymorphDBName, dbInfo.MorphCostCollectionName)
		}
		toSaveGene := oldGenesMap[mId.String()]
		oldGenesMap[mId.String()] = mEvent.OldGene.String()
		tokenToMorphEvent[mId.String()] = morphEvent

		g := metadata.Genome(mEvent.NewGene.String())
		metadataJson := (&g).Metadata(mId.String(), configService)

		rarityResult := CalulateRarityScore(metadataJson.Attributes, false)

		morphEntity := helpers.CreateMorphEntity(structs.PolymorphEvent{NewGene: mEvent.NewGene, OldGene: mEvent.OldGene, MorphId: mId}, metadataJson, false, rarityResult)

		res, err := handlers.PersistSinglePolymorph(morphEntity, dbInfo.PolymorphDBName, dbInfo.RarityCollectionName, toSaveGene, geneDifferences)
		if err != nil {
			log.Println(err)
			return err
		} else {
			log.Println(res)
		}

		if !hasTxMap {
			txMap = make(map[uint]bool)
			txState[morphEvent.TxHash.Hex()] = txMap
		}
		txState[morphEvent.TxHash.Hex()][morphEvent.Index] = true
		go handlers.SaveTransaction(dbInfo.PolymorphDBName, dbInfo.TransactionsCollectionName, models.Transaction{
			BlockNumber: morphEvent.BlockNumber,
			TxIndex:     morphEvent.TxIndex,
			TxHash:      morphEvent.TxHash.Hex(),
			LogIndex:    morphEvent.Index,
		})
	} else if txMap[morphEvent.Index] {
		log.Println("Already processed morph event! Skipping...")
	}

	mId := morphEvent.Topics[1].Big()

	result, err := instance.GeneOf(&bind.CallOpts{}, mId)
	if err != nil {
		log.Errorf("Error getting gene of id: %v", mId)
		return err
	}
	mEvent.NewGene = result

	g := metadata.Genome(mEvent.NewGene.String())
	genes := g.Genes()

	revGenes := metadata.ReverseGenesOrder(genes)

	imagesMetadataUrlV1 := os.Getenv("IMAGES_METADATA_URL_V1")
	//imagesMetadataUrlV2 := os.Getenv("IMAGES_METADATA_URL_V2")

	b := strings.Builder{}
	b.WriteString(imagesMetadataUrlV1)

	//d := strings.Builder{}
	//d.WriteString(imagesMetadataUrlV2)

	for _, gene := range revGenes {
		b.WriteString(gene)
		//d.WriteString(gene)
	}
	b.WriteString(".jpg")
	//d.WriteString(".jpg")

	// Currently, the front-end fetches imageURLs from the rarity-backend instead of from the Metadata API
	// So if the image doesn't exist, we query the V1 metadataPoly API to get it generated
	if !metadata.ImageExists(b.String()) {
		_, err := http.Get(imagesMetadataUrlV1 + mId.String())
		if err != nil {
			log.Errorf("Couldn't query images function. Original error: %v\n", err)
		} else {
			log.Info("Queried V1 Metadata with link: ", imagesMetadataUrlV1+mId.String())
		}
	}

	// query the V2 metadataPoly API to get it generated if image does not exist
	//if !metadata.ImageExists(d.String()) {
	//	_, err = http.Get(imagesMetadataUrlV2 + mId.String())
	//	if err != nil {
	//		log.Errorf("Couldn't query images function. Original error: %v\n", err)
	//	} else {
	//		log.Info("Queried V2 Metadata with link: ", imagesMetadataUrlV2+mId.String())
	//	}
	//}

	return nil
}

// processFinalMorphs is has almost the same logic as processInitialMorphs. It's idea is to process all the final mappings in oldGenesMap parameter.
//
// We're interested in morph events with event type 1 (0 is Morph, 2 is Transfer)
//
// What does this mean: At this point we've processed some morph events in processInitialMorphs but we still got some left in the oldGenesMap.
// Every gene in the map means that this is the latest morph event and must be compared with the current gene of the polymorph
//
// We compare the old and the new gene and create a history snapshot of the changes, persists the increment scramble/morph in the rarity collection.
//
// We don't persist the transaction as the transaction has already been persisted in processInitialMorphs.
//
// !! It's important to note which gene is passed as the new one and which as the old one in order to understand how the logic works.
func processFinalMorphs(morphEvent types.Log, ethClient *dlt.EthereumClient, contractAbi abi.ABI, instance *store.Store, configService *structs.ConfigService, dbInfo structs.DBInfo, oldGenesMap map[string]string, morphCostMap map[string]float32) error {
	var mEvent structs.MorphedEvent
	err := contractAbi.UnpackIntoInterface(&mEvent, constants.MorphEvent.Name, morphEvent.Data)
	if err != nil {
		log.Error("Error unpacking into interface when processing final morphs. ", err)
		return err
	}

	mId := morphEvent.Topics[1].Big()

	// This will get the newest gene
	result, err := instance.GeneOf(&bind.CallOpts{}, mId)
	if err != nil {
		log.Errorf("Error getting gene of id: %v", mId)
		return err
	}
	mEvent.NewGene = result

	g := metadata.Genome(mEvent.NewGene.String())
	genes := g.Genes()

	revGenes := metadata.ReverseGenesOrder(genes)

	polymorphImageUrl := os.Getenv("POLYMORPH_IMAGE_URL")

	b := strings.Builder{}
	b.WriteString(polymorphImageUrl)

	//polymorphImageUrl3D := os.Getenv("POLYMORPH_IMAGE_URL_3D")
	//
	//d := strings.Builder{}
	//d.WriteString(polymorphImageUrl3D)

	for _, gene := range revGenes {
		b.WriteString(gene)
		//d.WriteString(gene)
	}
	b.WriteString(".jpg")
	//d.WriteString(".jpg")

	imagesMetadataUrlV1 := os.Getenv("IMAGES_METADATA_URL_V1")

	// Currently, the front-end fetches imageURLs from the rarity-backend instead of from the Metadata API
	// So if the image doesn't exist, we query the V1 metadataPoly API to get it generated
	if !metadata.ImageExists(b.String()) {
		_, err := http.Get(imagesMetadataUrlV1 + mId.String())
		if err != nil {
			log.Errorf("Couldn't query images function. Original error: %v\n", err)
		} else {
			log.Info("Queried V1 Metadata with link: ", imagesMetadataUrlV1+mId.String())
		}
	}

	//imagesMetadataUrlV2 := os.Getenv("IMAGES_METADATA_URL_V2")

	// query the V2 metadataPoly API to get it generated if image does not exist
	//if !metadata.ImageExists(d.String()) {
	//	_, err = http.Get(imagesMetadataUrlV2 + mId.String())
	//	if err != nil {
	//		log.Errorf("Couldn't query images function. Original error: %v\n", err)
	//	} else {
	//		log.Info("Queried V2 Metadata with link: ", imagesMetadataUrlV2+mId.String())
	//	}
	//}

	newAttr, oldAttr := structs.Attribute{}, structs.Attribute{}
	geneIdx, geneDifferences := helpers.DetectGeneDifferences(oldGenesMap[mId.String()], mEvent.NewGene.String())
	if geneDifferences <= 2 {
		newAttr, oldAttr = helpers.GetAttribute(mEvent.NewGene.String(), oldGenesMap[mId.String()], geneIdx, configService)
	}
	block, err := ethClient.Client.HeaderByNumber(context.Background(), big.NewInt(int64(morphEvent.BlockNumber)))
	if err != nil {
		log.Error("Error getting block header. ", err)
		return err
	}
	polySnapshot := helpers.CreateMorphSnapshot(geneDifferences, mId.String(), mEvent.NewGene.String(), oldGenesMap[mId.String()], block.Time, oldAttr, newAttr, morphCostMap, configService)

	go handlers.SavePolymorphHistory(polySnapshot, dbInfo.PolymorphDBName, dbInfo.HistoryCollectionName)
	go handlers.SaveMorphPrice(models.MorphCost{TokenId: mId.String(), Price: morphCostMap[mId.String()]}, dbInfo.PolymorphDBName, dbInfo.MorphCostCollectionName)

	g = metadata.Genome(mEvent.NewGene.String())
	metadataPoly := (&g).Metadata(mId.String(), configService)

	rarityResult := CalulateRarityScore(metadataPoly.Attributes, false)

	if err != nil {
		log.Error("Error fetching chainID...")
	}

	morphEntity := helpers.CreateMorphEntity(structs.PolymorphEvent{NewGene: mEvent.NewGene, MorphId: mId}, metadataPoly, false, rarityResult)

	res, err := handlers.PersistSinglePolymorph(morphEntity, dbInfo.PolymorphDBName, dbInfo.RarityCollectionName, oldGenesMap[mId.String()], geneDifferences)
	if err != nil {
		log.Error("Error persisting single polymorph. ", err)
		return err
	} else {
		log.Info(res)
		return nil
	}
}
