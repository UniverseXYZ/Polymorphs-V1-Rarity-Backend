package helpers

import (
	"os"
	"polymorphs-rarity-v1/config"
	"polymorphs-rarity-v1/constants"
	"polymorphs-rarity-v1/metadata"
	"polymorphs-rarity-v1/models"
	"polymorphs-rarity-v1/structs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// CreateMorphEntity creates an entity which will be save in the rarities collection
func CreateMorphEntity(event structs.PolymorphEvent, metadata structs.Metadata, isVirgin bool, rarityResult structs.RarityResult) models.PolymorphEntity {
	var background, leftHand, rightHand, head, eye, torso, pants, feet, character structs.Attribute

	for _, attr := range metadata.Attributes {
		switch attr.TraitType {
		case constants.MorphAttriutes.Background:
			background = attr
		case constants.MorphAttriutes.Character:
			character = attr
		case constants.MorphAttriutes.RightHand:
			rightHand = attr
		case constants.MorphAttriutes.LeftHand:
			leftHand = attr
		case constants.MorphAttriutes.Footwear:
			feet = attr
		case constants.MorphAttriutes.Pants:
			pants = attr
		case constants.MorphAttriutes.Torso:
			torso = attr
		case constants.MorphAttriutes.Eyewear:
			eye = attr
		case constants.MorphAttriutes.Headwear:
			head = attr
		}
	}

	morphEntity := models.PolymorphEntity{
		TokenId:               int(event.MorphId.Int64()),
		Rank:                  0,
		CurrentGene:           event.NewGene.String(),
		Headwear:              head.Value,
		Eyewear:               eye.Value,
		Torso:                 torso.Value,
		Pants:                 pants.Value,
		Footwear:              feet.Value,
		LeftHand:              leftHand.Value,
		RightHand:             rightHand.Value,
		Character:             character.Value,
		Background:            background.Value,
		RarityScore:           rarityResult.ScaledRarity,
		IsVirgin:              isVirgin,
		ColorMismatches:       rarityResult.ColorMismatches,
		MainSetName:           rarityResult.MainSetName,
		MainMatchingTraits:    rarityResult.MainMatchingTraits,
		SecSetName:            rarityResult.SecSetName,
		SecMatchingTraits:     rarityResult.SecMatchingTraits,
		HasCompletedSet:       rarityResult.HasCompletedSet,
		HandsScaler:           rarityResult.HandsScaler,
		HandsSetName:          rarityResult.HandsSetName,
		MatchingHands:         rarityResult.MatchingHands,
		NoColorMismatchScaler: rarityResult.NoColorMismatchScaler,
		ColorMismatchScaler:   rarityResult.ColorMismatchScaler,
		VirginScaler:          rarityResult.VirginScaler,
		BaseRarity:            rarityResult.BaseRarity,
		ImageURL:              metadata.Image,
		Description:           metadata.Description,
		Name:                  metadata.Name,
	}
	if len(morphEntity.SecMatchingTraits) == 0 {
		morphEntity.SecMatchingTraits = []string{}
	}
	if len(morphEntity.MainMatchingTraits) == 0 {
		morphEntity.MainMatchingTraits = []string{}
	}
	return morphEntity
}

// CreateMorphSnapshot uses all the parameters in order to create a history snapshot of the polymorph. The morph cost mapping is updated depending on the morph type: Morph/Scramble.
//
// This snapshot is used to show the different variations each polymorph has gone through in the front end.
func CreateMorphSnapshot(geneDiff int, tokenId string, newGene string, oldGene string, timestamp uint64, oldAttr structs.Attribute, newAttr structs.Attribute, morphCostMap map[string]float32, configService *structs.ConfigService) models.PolymorphHistory {
	changeType, newAttrbiute, oldAttrubte := "", "", ""
	var newMorphCost float32 = 0
	morphCost := morphCostMap[tokenId]

	if morphCost == 0 {
		morphCost = config.SCRAMBLE_COST
	}

	if geneDiff <= 2 {
		changeType = "Morph"
		newAttrbiute = newAttr.Value
		oldAttrubte = oldAttr.Value
		newMorphCost = morphCost * 2
	} else {
		changeType = "Scramble"
		newAttrbiute = ""
		oldAttrubte = ""
		newMorphCost = config.SCRAMBLE_COST
	}
	morphCostMap[tokenId] = newMorphCost
	g := metadata.Genome(newGene)
	character := metadata.GetBaseGeneAttribute(newGene, configService)
	genes := g.Genes()

	polymorphImageUrl := os.Getenv("POLYMORPH_IMAGE_URL")

	imageUrl := strings.Builder{}
	imageUrl.WriteString(polymorphImageUrl)

	for _, gene := range genes {
		imageUrl.WriteString(gene)
	}

	imageUrl.WriteString(".jpg")
	tokenInt, _ := strconv.Atoi(tokenId)

	return models.PolymorphHistory{
		TokenId:           tokenInt,
		Type:              changeType,
		DateTime:          time.Unix(int64(timestamp), 0).UTC(),
		AttributeChanged:  oldAttr.TraitType,
		PreviousAttribute: oldAttrubte,
		NewAttribute:      newAttrbiute,
		Price:             morphCost,
		ImageURL:          imageUrl.String(),
		NewGene:           newGene,
		OldGene:           oldGene,
		Character:         character.Value,
	}
}

// SortMorphEvents sorts moprh events in chronological order(Block number -> Tx Index -> Log Index)
//
// If morph events aren't processed chronologically - the history snapshot of each morph event can have false information.
// We need the correct old state in order to calculate the correct differences in the gene
func SortMorphEvents(eventLogs []types.Log) {
	sort.Slice(eventLogs, func(i, j int) bool {
		curr := eventLogs[i]
		prev := eventLogs[j]

		if curr.BlockNumber < prev.BlockNumber {
			return true
		}

		if curr.BlockNumber > prev.BlockNumber {
			return false
		}

		if curr.TxIndex < prev.TxIndex {
			return true
		}

		if curr.TxIndex > prev.TxIndex {
			return false
		}

		if curr.Index < prev.Index {
			return true
		}

		if curr.Index > prev.Index {
			return false
		}
		return true
	})
}
