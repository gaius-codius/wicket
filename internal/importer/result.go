package importer

import "github.com/gaius-codius/wicket/internal/config"

// Skip is one source entry that did not become a profile, with a short
// reason for the import summary. It is config.Skip, so parse skips and
// collision skips are one list.
type Skip = config.Skip
