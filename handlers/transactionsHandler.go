package handlers

import (
	"context"
	"encoding/json"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"polymorphs-rarity-v1/db"
	"polymorphs-rarity-v1/models"

	"go.mongodb.org/mongo-driver/bson"
)

// GetTransactionsMapping fetches all records from the transactions collections. Returns a mapping of the records.
//
// The application has to know which morph events have already been processed in order to prevent duplicate false information stored in database
func GetTransactionsMapping(polymorphDBName string, transactionsColl string) (map[string]map[uint]bool, error) {
	collection, err := db.GetMongoDbCollection(polymorphDBName, transactionsColl)
	if err != nil {
		return nil, err
	}

	var transactions []models.Transaction
	results, err := collection.Find(context.Background(), bson.M{})
	if err != nil {
		log.Println("Error fetching transactions document. ", err)
	}
	results.All(context.Background(), &transactions)

	txMap := make(map[string]map[uint]bool)

	for _, tx := range transactions {
		nestedMap, ok := txMap[tx.TxHash]
		if !ok {
			nestedMap = make(map[uint]bool)
			txMap[tx.TxHash] = nestedMap
		}
		txMap[tx.TxHash][tx.LogIndex] = true
	}

	return txMap, nil
}

// SaveTransaction persists the processed transaction in the database
//
// If the application stops it will be able to load the processed event in memory from the database
func SaveTransaction(polymorphDBName string, transactionsColl string, transaction models.Transaction) {
	collection, err := db.GetMongoDbCollection(polymorphDBName, transactionsColl)
	if err != nil {
		log.Fatalln(err)
	}

	var bdoc interface{}
	json, _ := json.Marshal(transaction)
	bson.UnmarshalExtJSON(json, false, &bdoc)

	_, err = collection.InsertOne(context.Background(), bdoc)
	if err != nil {
		log.WithFields(log.Fields{"Original error: ": err}).Error("Error inserting transactions in db. ")
	}

	log.Infof("\nInserted new transaction in DB:\ntxHash: %v\nLogIndex: %v\n", transaction.TxHash, transaction.LogIndex)
}

// PersistMintEvents persists all the processed mints in the database in one go.
// Bulk writing to database saves a lot of time
func SaveTransactions(bsonDocs []interface{}, polymorphDBName string, transactionsCollectionName string) error {
	collection, err := db.GetMongoDbCollection(polymorphDBName, transactionsCollectionName)
	if err != nil {
		return err
	}

	opts := &options.InsertManyOptions{Ordered: &options.DefaultOrdered}
	opts.SetOrdered(false)

	res, err := collection.InsertMany(context.Background(), bsonDocs)
	if mongo.IsDuplicateKeyError(err) {
		log.Errorf("Duplicate key error when inserting into rarity collection! Will insert only non-dupicates...")
		log.Errorf("Original error: %v", err)
		return nil // returning NIL because non-duplicates will be inserted (setOrdered is set to false)
	} else if err != nil {
		log.Errorf("Error inserting many documents in MongoDB: %v", err)
		return err
	} else {
		log.Infof("Inserted %v transactions in DB", len(res.InsertedIDs))
		return nil
	}
}
