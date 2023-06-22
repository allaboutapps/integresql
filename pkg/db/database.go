package db

type Database struct {
	TemplateHash string         `json:"templateHash"`
	Config       DatabaseConfig `json:"config"`
}

type TestDatabase struct {
	Database `json:"database"`

	ID int `json:"id"`
}

type TemplateDatabase struct {
	Database `json:"database"`
}
