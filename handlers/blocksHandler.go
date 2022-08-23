package handlers

import (
	"context"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"polymorphs-rarity-v1/constants"
	"polymorphs-rarity-v1/db"
	"polymorphs-rarity-v1/models"
	"strconv"
)

// GetLastProcessedBlockNumber fetches the last processed block number from the block collection. At any point of the application there should be only one record in the collection
//
// If no collection or record exists - returns 0. This means the application will start processing from the beggining.
func GetLastProcessedBlockNumber(polymorphDBName string) (int64, error) {
	deploymentBlockNumberEth := os.Getenv("DEPLOYMENT_BLOCK_NUMBER_ETHEREUM")

	blocksCollectionNameEth := os.Getenv("BLOCKS_COLLECTION")

	ethBlockNumber, err := strconv.Atoi(deploymentBlockNumberEth)

	if err != nil {
		log.Error("%d is not a valid block number integer ", deploymentBlockNumberEth)
	}

	var collection *mongo.Collection

	collection, err = db.GetMongoDbCollection(polymorphDBName, blocksCollectionNameEth)

	if err != nil {
		return int64(ethBlockNumber), err
	}

	lastBlock := collection.FindOne(context.Background(), bson.M{})

	if lastBlock.Err() != nil {
		return 0, lastBlock.Err()
	}

	var result bson.M
	lastBlock.Decode(&result)

	if result == nil {
		return 0, err
	}

	lastProcessedBlockNumber := result[constants.BlockFieldNames.Number]
	block := lastProcessedBlockNumber.(int64)
	if block < int64(ethBlockNumber) {
		return int64(ethBlockNumber), nil
	}
	return block, nil
}

// CreateOrUpdateLastProcessedBlock persists the passed block number in the parameters to the block collection. At any point of the application there should be only one record in the collection
//
// If no collection or records exists - it will create a new one.
func CreateOrUpdateLastProcessedBlock(number uint64, polymorphDBName string, blocksCollectionName string) (int, error) {
	collection, err := db.GetMongoDbCollection(polymorphDBName, blocksCollectionName)
	if err != nil {
		return -1, err
	}

	entity := models.ProcessedBlockEntity{Number: number}

	update := bson.M{
		"$set": entity,
	}

	// This option will create new entity if no matching is found
	opts := options.Update().SetUpsert(true)

	objID, _ := primitive.ObjectIDFromHex(strconv.FormatInt(0, 16))
	filter := bson.M{constants.BlockFieldNames.ObjId: objID}

	_, err = collection.UpdateOne(context.Background(), filter, update, opts)

	if err != nil {
		return -1, err
	}

	lastPersistedBlock, err := strconv.Atoi(strconv.FormatUint(number, 10))

	if err != nil {
		log.Errorf("Unable to convert last persisted block number [%v] to int", lastPersistedBlock)
		return -1, err
	}

	return lastPersistedBlock, nil
}
