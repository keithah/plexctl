package api

type Container struct {
	Size      int              `json:"size,omitempty"`
	Title     string           `json:"title,omitempty"`
	Offset    int              `json:"offset,omitempty"`
	TotalSize int              `json:"totalSize,omitempty"`
	Items     []map[string]any `json:"Metadata,omitempty"`
}
