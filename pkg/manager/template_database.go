package manager

type TemplateConfig struct {
	TemplateHash string         `json:"templateHash"`
	Config       DatabaseConfig `json:"config"`
	nextTestID   int
}

func (c TemplateConfig) IsEmpty() bool {
	return c.TemplateHash == ""
}

type TemplateDatabase struct {
	Database `json:"database"`

	nextTestID    int
	testDatabases []*TestDatabase
}
