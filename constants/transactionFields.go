package constants

import "polymorphs-rarity-v1/structs"

var TxFieldNames = structs.TransactionFieldNames{
	ObjId:       "_id",
	BlockNumber: "blocknumber",
	TxIndex:     "txindex",
	TxHash:      "txhash",
	LogIndex:    "logindex",
}
