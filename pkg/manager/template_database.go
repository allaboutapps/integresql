package manager

type TemplateDatabase struct {
	Database `json:"database"`

	nextTestID    int
	testDatabases []*TestDatabase
}
