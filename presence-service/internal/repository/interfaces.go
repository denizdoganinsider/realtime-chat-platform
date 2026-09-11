package repository

type PresenceRepositoryInterface interface {
	SetOnline(room string, userID int64, instanceID string) error
	SetOffline(room string, userID int64, instanceID string) error
	Refresh(room string, instanceID string, userIDs []int64) error
	ListOnline(room string) ([]int64, error)
	ListRooms() ([]RoomCount, error)
}

type RoomCount struct {
	Name  string
	Count int
}
