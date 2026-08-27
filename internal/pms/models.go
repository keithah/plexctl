package pms

type Identity struct {
	MediaContainer IdentityContainer `json:"MediaContainer"`
}
type IdentityContainer struct {
	Size              int    `json:"size"`
	MachineIdentifier string `json:"machineIdentifier"`
	Version           string `json:"version"`
	Platform          string `json:"platform"`
	PlatformVersion   string `json:"platformVersion"`
	Title             string `json:"title"`
}
type Root struct {
	MediaContainer map[string]any `json:"MediaContainer"`
}
type LibrarySections struct {
	MediaContainer struct {
		Size      int         `json:"size"`
		Directory []Directory `json:"Directory"`
	} `json:"MediaContainer"`
}
type Directory struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Agent   string `json:"agent"`
	Scanner string `json:"scanner"`
}
type MetadataContainer struct {
	MediaContainer struct {
		Size     int        `json:"size"`
		Metadata []Metadata `json:"Metadata"`
	} `json:"MediaContainer"`
}
type Metadata struct {
	RatingKey        string `json:"ratingKey"`
	Key              string `json:"key"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	GrandparentTitle string `json:"grandparentTitle"`
	ParentTitle      string `json:"parentTitle"`
	Year             int    `json:"year"`
	Duration         int64  `json:"duration"`
	ViewOffset       int64  `json:"viewOffset"`
}
type SessionContainer struct {
	MediaContainer struct {
		Size     int       `json:"size"`
		Metadata []Session `json:"Metadata"`
	} `json:"MediaContainer"`
}
type Session struct {
	Session struct {
		ID string `json:"id"`
	} `json:"session"`
	RatingKey        string `json:"ratingKey"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	GrandparentTitle string `json:"grandparentTitle"`
	ParentTitle      string `json:"parentTitle"`
	ViewOffset       int64  `json:"viewOffset"`
	Duration         int64  `json:"duration"`
}
