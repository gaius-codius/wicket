package importer

// Skip is one source entry that did not become a profile, with a short
// reason for the import summary.
type Skip struct {
	Name   string
	Reason string
}
