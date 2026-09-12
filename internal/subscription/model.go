package subscription

import "encoding/json"

type Format string

const (
	FormatSingBox Format = "sing-box"
	FormatClash   Format = "clash"
	FormatURI     Format = "uri"
)

type NodeSpec struct {
	ID        string
	Tag       string
	Type      string
	Format    Format
	Options   json.RawMessage
	SourceIDs []string
}

type Source struct {
	ID  string
	URL string
}

type Snapshot struct {
	Nodes        []NodeSpec
	SourceErrors map[string]string
}
