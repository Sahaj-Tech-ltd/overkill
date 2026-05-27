package page

type PageID int

const (
	ChatPageID PageID = iota
)

type PageChangeMsg struct {
	ID PageID
}
