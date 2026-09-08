package pms

import "encoding/json"

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
	MediaContainer playlistMediaContainer `json:"MediaContainer"`
}
type playlistMediaContainer struct {
	Size         int `json:"size"`
	Offset       int `json:"offset"`
	TotalSize    int `json:"totalSize"`
	offsetSet    bool
	totalSizeSet bool
	Metadata     []Playlist `json:"Metadata"`
}

func (m *playlistMediaContainer) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Size      int        `json:"size"`
		Offset    *int       `json:"offset"`
		TotalSize *int       `json:"totalSize"`
		Metadata  []Playlist `json:"Metadata"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	m.Size, m.Metadata = decoded.Size, decoded.Metadata
	m.offsetSet, m.totalSizeSet = decoded.Offset != nil, decoded.TotalSize != nil
	if m.offsetSet {
		m.Offset = *decoded.Offset
	}
	if m.totalSizeSet {
		m.TotalSize = *decoded.TotalSize
	}
	return nil
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

type LibrarySection struct {
	MediaContainer struct {
		Title string `json:"title1"`
		Key   string `json:"key"`
		Type  string `json:"type"`
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
	MediaContainer metadataMediaContainer `json:"MediaContainer"`
}

type metadataMediaContainer struct {
	Size         int `json:"size"`
	Offset       int `json:"offset"`
	TotalSize    int `json:"totalSize"`
	offsetSet    bool
	totalSizeSet bool
	Metadata     []Metadata `json:"Metadata"`
}

func (m *metadataMediaContainer) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Size      int        `json:"size"`
		Offset    *int       `json:"offset"`
		TotalSize *int       `json:"totalSize"`
		Metadata  []Metadata `json:"Metadata"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	m.Size = decoded.Size
	m.offsetSet = decoded.Offset != nil
	if m.offsetSet {
		m.Offset = *decoded.Offset
	} else {
		m.Offset = 0
	}
	m.Metadata = decoded.Metadata
	m.totalSizeSet = decoded.TotalSize != nil
	if m.totalSizeSet {
		m.TotalSize = *decoded.TotalSize
	} else {
		m.TotalSize = 0
	}
	return nil
}

type Metadata struct {
	RatingKey           string  `json:"ratingKey"`
	Key                 string  `json:"key"`
	Type                string  `json:"type"`
	Title               string  `json:"title"`
	Thumb               string  `json:"thumb"`
	GUID                []GUID  `json:"Guid"`
	GrandparentTitle    string  `json:"grandparentTitle"`
	ParentTitle         string  `json:"parentTitle"`
	Year                int     `json:"year"`
	Duration            *int64  `json:"duration"`
	ViewedAt            *int64  `json:"viewedAt"`
	LibrarySectionID    string  `json:"librarySectionID"`
	LibrarySectionTitle string  `json:"librarySectionTitle"`
	AccountID           int64   `json:"accountID"`
	AccountTitle        string  `json:"accountTitle"`
	ViewOffset          int64   `json:"viewOffset"`
	Media               []Media `json:"Media"`
}
type GUID struct {
	ID string `json:"id"`
}
type Media struct {
	Part []Part `json:"Part"`
}
type Part struct {
	Key         string `json:"key"`
	Size        *int64 `json:"size"`
	ChangedAt   *int64 `json:"changedAt"`
	ChangeStamp *int64 `json:"changestamp"`
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
	RatingKey        string           `json:"ratingKey"`
	Type             string           `json:"type"`
	Title            string           `json:"title"`
	GrandparentTitle string           `json:"grandparentTitle"`
	ParentTitle      string           `json:"parentTitle"`
	ViewOffset       int64            `json:"viewOffset"`
	Duration         int64            `json:"duration"`
	User             SessionUser      `json:"User"`
	Player           SessionPlayer    `json:"Player"`
	TranscodeSession TranscodeSession `json:"TranscodeSession"`
	Media            []SessionMedia   `json:"Media"`
}

// SessionMedia contains the documented per-stream delivery decisions from an
// active-session response. Pointers preserve omission so diagnostics can fail
// closed rather than treating malformed metadata as a delivery state.
type SessionMedia struct {
	VideoDecision    *string `json:"videoDecision"`
	AudioDecision    *string `json:"audioDecision"`
	SubtitleDecision *string `json:"subtitleDecision"`
}

type SessionUser struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type SessionPlayer struct {
	MachineIdentifier string `json:"machineIdentifier"`
	Title             string `json:"title"`
	Platform          string `json:"platform"`
	Address           string `json:"address"`
}

type TranscodeSession struct {
	Key       string   `json:"key"`
	Throttled *bool    `json:"throttled"`
	Complete  *bool    `json:"complete"`
	Speed     *float64 `json:"speed"`
	Progress  *float64 `json:"progress"`
}

type ActivitiesContainer struct {
	MediaContainer struct {
		Size     int        `json:"size"`
		Activity []Activity `json:"Activity"`
	} `json:"MediaContainer"`
	mediaContainerSet bool
}

func (c *ActivitiesContainer) UnmarshalJSON(data []byte) error {
	type wire ActivitiesContainer
	var decoded struct {
		MediaContainer *json.RawMessage `json:"MediaContainer"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.MediaContainer == nil {
		return nil
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = ActivitiesContainer(value)
	c.mediaContainerSet = true
	return nil
}

type Activity struct {
	UUID        string   `json:"uuid"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Progress    *float64 `json:"progress"`
	Cancellable *bool    `json:"cancellable"`
}

type ButlerContainer struct {
	MediaContainer struct {
		Size       int          `json:"size"`
		ButlerTask []ButlerTask `json:"ButlerTask"`
	} `json:"MediaContainer"`
	mediaContainerSet bool
}

func (c *ButlerContainer) UnmarshalJSON(data []byte) error {
	type wire ButlerContainer
	var decoded struct {
		MediaContainer *json.RawMessage `json:"MediaContainer"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.MediaContainer == nil {
		return nil
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = ButlerContainer(value)
	c.mediaContainerSet = true
	return nil
}

type ButlerTask struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Enabled  *bool  `json:"enabled"`
	Interval *int64 `json:"interval"`
	Schedule string `json:"schedule"`
}

type UpdaterStatusContainer struct {
	MediaContainer    UpdaterStatus `json:"MediaContainer"`
	mediaContainerSet bool
}

func (c *UpdaterStatusContainer) UnmarshalJSON(data []byte) error {
	type wire UpdaterStatusContainer
	var decoded struct {
		MediaContainer *json.RawMessage `json:"MediaContainer"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.MediaContainer == nil {
		return nil
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = UpdaterStatusContainer(value)
	c.mediaContainerSet = true
	return nil
}

type UpdaterStatus struct {
	CanInstall  *bool  `json:"canInstall"`
	Version     string `json:"version"`
	ReleaseDate string `json:"releaseDate"`
	DownloadURL string `json:"downloadURL"`
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
