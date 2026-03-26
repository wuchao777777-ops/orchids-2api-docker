package store

func BuildV0SeedModels() []Model {
	return []Model{
		{
			ID:        "122",
			Channel:   "V0",
			ModelID:   "v0-auto",
			Name:      "v0 Auto",
			Status:    ModelStatusAvailable,
			IsDefault: false,
			SortOrder: 0,
		},
		{
			ID:        "123",
			Channel:   "V0",
			ModelID:   "v0-mini",
			Name:      "v0 Mini",
			Status:    ModelStatusAvailable,
			IsDefault: false,
			SortOrder: 1,
		},
		{
			ID:        "124",
			Channel:   "V0",
			ModelID:   "v0-pro",
			Name:      "v0 Pro",
			Status:    ModelStatusAvailable,
			IsDefault: false,
			SortOrder: 2,
		},
		{
			ID:        "125",
			Channel:   "V0",
			ModelID:   "v0-max",
			Name:      "v0 Max",
			Status:    ModelStatusAvailable,
			IsDefault: true,
			SortOrder: 3,
		},
		{
			ID:        "126",
			Channel:   "V0",
			ModelID:   "v0-max-fast",
			Name:      "v0 Max Fast",
			Status:    ModelStatusAvailable,
			IsDefault: false,
			SortOrder: 4,
		},
	}
}
