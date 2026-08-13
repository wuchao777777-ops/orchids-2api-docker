package store

import (
	"strconv"

	"orchids-api/internal/modelpolicy"
)

func buildPuterSeedModels() []Model {
	modelIDs := modelpolicy.LatestPuterModelIDs()

	models := make([]Model, 0, len(modelIDs))
	for i, modelID := range modelIDs {
		models = append(models, Model{
			ID:        strconv.Itoa(109 + i),
			Channel:   "Puter",
			ModelID:   modelID,
			Name:      modelID,
			Status:    ModelStatusAvailable,
			IsDefault: modelID == modelpolicy.DefaultPuterModelID,
			SortOrder: i,
		})
	}
	return models
}
