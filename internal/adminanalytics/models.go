package adminanalytics

import "time"

const (
	DefaultWindowDays = 30
	MaxWindowDays     = 366
	Timezone          = "Asia/Shanghai"
)

type Window struct {
	Start       time.Time
	End         time.Time
	StartUTC    time.Time
	EndUTC      time.Time
	Days        int
	GeneratedAt time.Time
}

func (w Window) StartDate() string { return w.Start.Format(time.DateOnly) }
func (w Window) EndDate() string   { return w.End.Format(time.DateOnly) }

type OverviewMetrics struct {
	TotalUsers                int64
	TotalGames                int64
	NewUsers                  int64
	DAU                       int64
	WAU                       int64
	MAU                       int64
	DAUMAURate                float64
	FirstMatchConversionRate  float64
	D1RetentionRate           float64
	D7RetentionRate           float64
	D30RetentionRate          float64
	GamesStarted              int64
	GamesCompleted            int64
	GamesAbandoned            int64
	CompletionRate            float64
	GameDurationSeconds       int64
	PlayerDurationSeconds     int64
	AverageGameDurationSecond int64
	AveragePlayerDuration     int64
}

type DailyOverview struct {
	Date                  string
	NewUsers              int64
	ActiveUsers           int64
	GamesStarted          int64
	GamesCompleted        int64
	GamesAbandoned        int64
	GameDurationSeconds   int64
	PlayerDurationSeconds int64
}

type Overview struct {
	Window  Window
	Metrics OverviewMetrics
	Daily   []DailyOverview
}

type DateRangeQuery struct {
	StartDate string
	EndDate   string
}

type GamesQuery struct {
	DateRange DateRangeQuery
	AllTime   bool
}

type User struct {
	ID                    int64
	DisplayName           string
	Email                 string
	Avatar                string
	CreatedAt             time.Time
	FirstActiveDate       string
	LastActiveAt          *time.Time
	ActiveDays            int64
	GamesPlayed           int64
	PlayerDurationSeconds int64
	FavoriteGameType      string
}

type UsersPage struct {
	Window   Window
	Users    []User
	Page     int
	PageSize int
	Total    int64
}

type UserDailyActivity struct {
	Date                  string
	GamesPlayed           int64
	PlayerDurationSeconds int64
}

type UserGameActivity struct {
	GameType             string
	GamesPlayed          int64
	ActiveDays           int64
	PlayerDurationSecond int64
	ShareRate            float64
}

type UserActivity struct {
	Window Window
	User   User
	Daily  []UserDailyActivity
	Games  []UserGameActivity
}

type GameActivity struct {
	GameType                string
	ActiveUsers             int64
	GamesStarted            int64
	GamesCompleted          int64
	GamesAbandoned          int64
	CompletionRate          float64
	GameDurationSeconds     int64
	PlayerDurationSeconds   int64
	AverageGameDuration     int64
	AverageHumanPlayerCount float64
}

type Games struct {
	Window Window
	Games  []GameActivity
}

type LiveGame struct {
	GameType      string
	ActiveRooms   int64
	ActiveMatches int64
	OnlinePlayers int64
}

type LivePlayer struct {
	UserID      string
	DisplayName string
	Avatar      string
	Online      bool
	IsOwner     bool
}

type LiveRoom struct {
	RoomID        string
	MatchID       string
	GameType      string
	Status        string
	OwnerUserID   string
	HumanPlayers  int64
	OnlinePlayers int64
	StartedAt     time.Time
	Players       []LivePlayer
}

type Live struct {
	GeneratedAt   time.Time
	ActiveRooms   int64
	ActiveMatches int64
	OnlinePlayers int64
	Games         []LiveGame
	Rooms         []LiveRoom
}

type UserQuery struct {
	Page      int
	PageSize  int
	Query     string
	SortBy    string
	SortOrder string
}
