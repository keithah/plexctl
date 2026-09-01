package pms

type SearchContainer struct {
	MediaContainer struct {
		Size int   `json:"size"`
		Hub  []Hub `json:"Hub"`
	} `json:"MediaContainer"`
}

type Hub struct {
	HubIdentifier string      `json:"hubIdentifier"`
	Title         string      `json:"title"`
	Type          string      `json:"type"`
	Size          int         `json:"size"`
	More          bool        `json:"more"`
	Metadata      []Metadata  `json:"Metadata"`
	Directory     []Directory `json:"Directory"`
}

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
	MediaContainer ServerInfo `json:"MediaContainer"`
}
type ServerInfo struct {
	FriendlyName      string            `json:"friendlyName"`
	Version           string            `json:"version"`
	MachineIdentifier string            `json:"machineIdentifier"`
	Platform          string            `json:"platform"`
	PlatformVersion   string            `json:"platformVersion"`
	TranscoderVideo   bool              `json:"transcoderVideo"`
	TranscoderAudio   bool              `json:"transcoderAudio"`
	HubSearch         bool              `json:"hubSearch"`
	Livetv            int               `json:"livetv"`
	MyPlex            bool              `json:"myPlex"`
	Directory         []ServerDirectory `json:"Directory"`
}
type ServerDirectory struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Count int    `json:"count"`
}
type PlaylistContainer struct {
	MediaContainer struct {
		Size     int        `json:"size"`
		Metadata []Playlist `json:"Metadata"`
	} `json:"MediaContainer"`
}
type Playlist struct {
	Metadata
	Composite           string `json:"composite"`
	LeafCount           int    `json:"leafCount"`
	PlaylistType        string `json:"playlistType"`
	ReadOnly            bool   `json:"readOnly"`
	Smart               bool   `json:"smart"`
	SpecialPlaylistType string `json:"specialPlaylistType"`
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
	RatingKey        string  `json:"ratingKey"`
	Key              string  `json:"key"`
	Type             string  `json:"type"`
	Title            string  `json:"title"`
	GrandparentTitle string  `json:"grandparentTitle"`
	ParentTitle      string  `json:"parentTitle"`
	Year             int     `json:"year"`
	Duration         int64   `json:"duration"`
	ViewOffset       int64   `json:"viewOffset"`
	Media            []Media `json:"Media"`
}
type Media struct {
	Part []Part `json:"Part"`
}
type Part struct {
	Key string `json:"key"`
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

type DownloadQueueContainer struct {
	MediaContainer struct {
		Size          int                 `json:"size"`
		DownloadQueue []DownloadQueue     `json:"DownloadQueue"`
		Items         []DownloadQueueItem `json:"DownloadQueueItem"`
	} `json:"MediaContainer"`
}
type DownloadQueue struct {
	ID        int    `json:"id"`
	ItemCount int    `json:"itemCount"`
	Status    string `json:"status"`
}
type DownloadQueueItem struct {
	ID               int            `json:"id"`
	Status           string         `json:"status"`
	Title            string         `json:"title"`
	RatingKey        string         `json:"ratingKey"`
	DecisionResult   map[string]any `json:"DecisionResult"`
	TranscodeSession map[string]any `json:"TranscodeSession"`
}
type TranscodeContainer struct {
	MediaContainer map[string]any `json:"MediaContainer"`
}
