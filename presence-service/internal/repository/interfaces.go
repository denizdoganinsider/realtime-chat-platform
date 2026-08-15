package repository

type PresenceRepositoryInterface interface {
	SetOnline(room string, userID int64) error
	SetOffline(room string, userID int64) error
	Refresh(room string, userIDs []int64) error
	ListOnline(room string) ([]int64, error)
}
