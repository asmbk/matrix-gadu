package gadu

import "fmt"

// Status is the presence state of a GG account.
type Status int

const (
	StatusOffline Status = iota
	StatusAvailable
	StatusFreeForChat
	StatusBusy
	StatusDND
	StatusInvisible
	StatusBlocked
)

var statusNames = map[Status]string{
	StatusOffline:     "offline",
	StatusAvailable:   "available",
	StatusFreeForChat: "ffc",
	StatusBusy:        "busy",
	StatusDND:         "dnd",
	StatusInvisible:   "invisible",
	StatusBlocked:     "blocked",
}

func (s Status) String() string {
	if n, ok := statusNames[s]; ok {
		return n
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// ParseStatus parses a status that the account owner can set.
func ParseStatus(s string) (Status, error) {
	switch s {
	case "available":
		return StatusAvailable, nil
	case "ffc":
		return StatusFreeForChat, nil
	case "busy":
		return StatusBusy, nil
	case "dnd":
		return StatusDND, nil
	case "invisible":
		return StatusInvisible, nil
	}
	return 0, fmt.Errorf("invalid status %q (use available, ffc, busy, dnd or invisible)", s)
}
