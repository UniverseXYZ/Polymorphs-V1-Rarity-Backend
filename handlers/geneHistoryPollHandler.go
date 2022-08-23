package handlers

import (
	"context"
	"encoding/json"
	"log"
	"polymorphs-rarity-v1/db"
	"polymorphs-rarity-v1/models"
	"strconv"

	"go.mongodb.org/mongo-driver/bson"
)

// SavePolymorphHistory persists the polymorph history snapshot to the database.
func SavePolymorphHistory(entity models.PolymorphHistory, polymorphDBName string, historyCollectionName string) error {
	collection, err := db.GetMongoDbCollection(polymorphDBName, historyCollectionName)

	if err != nil {
		return err
	}

	var bdoc interface{}
	json, _ := json.Marshal(entity)
	bson.UnmarshalExtJSON(json, false, &bdoc)

	_, err = collection.InsertOne(context.Background(), bdoc)
	if err != nil {
		log.Println("Error inserting a document in History collection. ", err)
	}

	log.Println("Inserted history snapshot for polymorph #" + strconv.Itoa(entity.TokenId))
	return nil
}
